// Command uv28prog programs a Baofeng UV-28 Plus / UV-28 Pro (and the
// closely related UV-21 Pro / UV-32 Pro / AR-152 Pro) over Bluetooth LE,
// from Windows, without the vendor's Android app.
//
// Offline commands (export/import/show) work on any platform; the Bluetooth
// commands (scan/read/write) are built into the Windows binary.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"uv28prog/internal/radio"
)

const usage = `uv28prog - Baofeng UV-28 Plus/Pro Bluetooth programmer

USAGE
  uv28prog <command> [flags]

BLUETOOTH COMMANDS (Windows)
  scan                    List nearby radios (advertised as "walkie-talkie").
  read   [flags]          Read the radio into an image file (a full backup).
  write  [flags]          Write channels to the radio (add -full for all regions).

OFFLINE COMMANDS (any platform)
  export [flags]          Export channels from an image to a CSV you can edit.
  import [flags]          Merge an edited CSV back into an image file.
  show   -i image.json    Print the channel list from an image.
  diff   A.json B.json    Compare two images (which regions/channels differ).

COMMON FLAGS
  -i FILE        input image file (JSON)
  -o FILE        output file
  -addr MAC      connect to a specific radio address (from 'scan')
  -timeout DUR   scan/connect timeout (default 20s)
  -v             verbose: log every Bluetooth packet
  -force-mismatch  allow write when image model differs from radio

TYPICAL WORKFLOW
  1. uv28prog read  -o mine.json            (back up the radio first!)
  2. uv28prog export -i mine.json -o ch.csv
  3. edit ch.csv in a spreadsheet
  4. uv28prog import -i mine.json -csv ch.csv -o new.json
  5. uv28prog write  -i new.json -yes       (keeps an automatic backup)

This talks to your own radio over its built-in Bluetooth module. Always keep
the backup written by 'read' so you can restore the radio if needed.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "scan":
		err = cmdScan(os.Args[2:])
	case "read":
		err = cmdRead(os.Args[2:])
	case "write":
		err = cmdWrite(os.Args[2:])
	case "export":
		err = cmdExport(os.Args[2:])
	case "import":
		err = cmdImport(os.Args[2:])
	case "show":
		err = cmdShow(os.Args[2:])
	case "diff":
		err = cmdDiff(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if errors.Is(err, flag.ErrHelp) {
		return // "<command> -h": the command's flags were printed
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// parseFlags parses a command's flags. The flag package's own printing is
// silenced so that main reports an error exactly once; -h prints the
// command's flags and returns flag.ErrHelp so main exits cleanly.
//
// Leftover positional arguments are an error: the flag package stops at the
// first one, so it and every flag after it would otherwise be ignored
// (e.g. "import -i mine.json -csv ch.csv new.json" would overwrite mine.json).
func parseFlags(fs *flag.FlagSet, args []string) error {
	fs.SetOutput(io.Discard)
	err := fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Printf("Usage: uv28prog %s [flags]\n\nFlags:\n", fs.Name())
		fs.SetOutput(os.Stdout)
		fs.PrintDefaults()
		return flag.ErrHelp
	}
	if err != nil {
		return fmt.Errorf("%s: %w (see 'uv28prog %s -h')", fs.Name(), err, fs.Name())
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("%s: unexpected argument %q; give files with flags such as -i/-o (see 'uv28prog %s -h')",
			fs.Name(), fs.Arg(0), fs.Name())
	}
	return nil
}

// verboseFlag registers -v and its long form -verbose.
func verboseFlag(fs *flag.FlagSet, usage string) *bool {
	v := fs.Bool("v", false, usage)
	fs.BoolVar(v, "verbose", false, "same as -v")
	return v
}

func logger(verbose bool) radio.Logger {
	if !verbose {
		return func(string, ...any) {}
	}
	return func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "[ble] "+format+"\n", args...)
	}
}

func progressBar(label string) radio.Progress {
	last := -1
	return func(done, total int) {
		pct := done * 100 / total
		if pct != last {
			last = pct
			fmt.Fprintf(os.Stderr, "\r%s %3d%% (%d/%d blocks)", label, pct, done, total)
			if done == total {
				fmt.Fprintln(os.Stderr)
			}
		}
	}
}

func cmdScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	addr := fs.String("addr", "", "connect to a specific radio address (from 'scan')")
	timeout := fs.Duration("timeout", 20*time.Second, "scan/connect timeout")
	verbose := verboseFlag(fs, "verbose: log every Bluetooth packet")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Scanning for radios (turn the radio on, keep it close)...")
	found, err := Scan(*timeout, *addr, logger(*verbose))
	if err != nil {
		return err
	}
	if len(found) == 0 {
		fmt.Println("No radios found.")
		return nil
	}
	for _, r := range found {
		fmt.Printf("  %s  %-16s  rssi=%d\n", r.Address, r.Name, r.RSSI)
	}
	fmt.Printf("\n%d radio(s). Use: uv28prog read -addr <address> -o mine.json\n", len(found))
	return nil
}

func cmdRead(args []string) error {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	out := fs.String("o", "", "output image file (JSON)")
	fs.StringVar(out, "out", "", "output image file (JSON)")
	addr := fs.String("addr", "", "connect to a specific radio address")
	timeout := fs.Duration("timeout", 20*time.Second, "scan/connect timeout")
	verbose := verboseFlag(fs, "verbose: log every Bluetooth packet")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *out == "" {
		*out = fmt.Sprintf("uv28-%s.json", time.Now().Format("20060102-150405"))
	}
	tr, err := Connect(*addr, *timeout, logger(*verbose))
	if err != nil {
		return err
	}
	defer tr.Close()
	d := radio.NewDriver(tr, logger(*verbose))
	fmt.Fprintln(os.Stderr, "Reading radio...")
	im, err := d.ReadImage(progressBar("read"))
	if err != nil {
		return err
	}
	data, err := im.MarshalFile()
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}
	n := countChannels(im)
	fmt.Printf("Read OK. Model %q, %d channels. Saved to %s\n", im.Ident, n, *out)
	return nil
}

func cmdWrite(args []string) error {
	fs := flag.NewFlagSet("write", flag.ContinueOnError)
	in := fs.String("i", "", "input image file (JSON)")
	fs.StringVar(in, "in", "", "input image file (JSON)")
	addr := fs.String("addr", "", "connect to a specific radio address")
	backup := fs.String("backup", "", "path for the automatic backup (default: timestamped)")
	timeout := fs.Duration("timeout", 20*time.Second, "scan/connect timeout")
	verbose := verboseFlag(fs, "verbose: log every Bluetooth packet")
	yes := fs.Bool("yes", false, "confirm the write (required)")
	fs.BoolVar(yes, "y", false, "confirm the write (required)")
	full := fs.Bool("full", false, "write all regions (advanced; default is channels only)")
	noBackup := fs.Bool("no-backup", false, "skip the automatic backup")
	forceMismatch := fs.Bool("force-mismatch", false, "allow writing even if image model differs from radio")
	// Kept so existing commands keep working; channel-only is now the default.
	fs.Bool("only-channels", false, "ignored (writing only channels is the default; see -full)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *in == "" {
		return fmt.Errorf("write needs -i <image.json>")
	}
	im, err := loadImage(*in)
	if err != nil {
		return err
	}
	if !*yes {
		return fmt.Errorf("writing to the radio changes it. Re-run with -yes to confirm.\n" +
			"  (a full backup of the current radio is saved automatically first)")
	}

	// The radio expects one operation per Bluetooth session, so the backup
	// read and the write each use their own fresh connection (just like
	// running two separate commands).
	if !*noBackup {
		bpath := *backup
		if bpath == "" {
			bpath = fmt.Sprintf("uv28-backup-%s.json", time.Now().Format("20060102-150405"))
		}
		fmt.Fprintln(os.Stderr, "Backing up current radio contents first...")
		tr, err := Connect(*addr, *timeout, logger(*verbose))
		if err != nil {
			return err
		}
		cur, err := radio.NewDriver(tr, logger(*verbose)).ReadImage(progressBar("backup"))
		tr.Close()
		if err != nil {
			return fmt.Errorf("backup read failed, aborting before any write: %w", err)
		}
		bdata, err := cur.MarshalFile()
		if err != nil {
			return fmt.Errorf("could not marshal backup, aborting: %w", err)
		}
		if err := os.WriteFile(bpath, bdata, 0o644); err != nil {
			return fmt.Errorf("could not save backup, aborting: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Backup saved to %s\n", bpath)
		// Let the module drop the first session and re-advertise.
		time.Sleep(2 * time.Second)
	}

	// By default write only the channel region: it round-trips exactly, while
	// the VFO (0x8000) and settings (0x9000) regions are managed by the radio
	// and must not be overwritten. -full writes every region (advanced).
	only := channelBlocks()
	scope := "channels"
	if *full {
		only = nil
		scope = "all regions"
	}
	fmt.Fprintf(os.Stderr, "Writing to radio (%s)...\n", scope)
	tr, err := Connect(*addr, *timeout, logger(*verbose))
	if err != nil {
		return err
	}
	defer tr.Close()
	if err := radio.NewDriver(tr, logger(*verbose)).WriteImage(im, only, *forceMismatch, progressBar("write")); err != nil {
		return err
	}
	fmt.Println("Write OK.")
	return nil
}

func cmdExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	in := fs.String("i", "", "input image file (JSON)")
	fs.StringVar(in, "in", "", "input image file (JSON)")
	out := fs.String("o", "", "output CSV file")
	fs.StringVar(out, "out", "", "output CSV file")
	all := fs.Bool("all", false, "include empty channels")
	verboseFlag(fs, "accepted for consistency (offline commands have nothing to log)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *in == "" {
		return fmt.Errorf("export needs -i <image.json>")
	}
	im, err := loadImage(*in)
	if err != nil {
		return err
	}
	if *out == "" {
		*out = strings.TrimSuffix(filepath.Base(*in), filepath.Ext(*in)) + ".csv"
	}
	fh, err := os.Create(*out)
	if err != nil {
		return err
	}
	defer fh.Close()
	if err := im.ExportCSV(fh, *all); err != nil {
		return err
	}
	fmt.Printf("Exported %d channels to %s\n", countChannels(im), *out)
	return nil
}

func cmdImport(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	in := fs.String("i", "", "input image file (JSON)")
	fs.StringVar(in, "in", "", "input image file (JSON)")
	csvPath := fs.String("csv", "", "CSV file with channel edits")
	out := fs.String("o", "", "output image file (default: overwrite -i)")
	fs.StringVar(out, "out", "", "output image file (default: overwrite -i)")
	verboseFlag(fs, "accepted for consistency (offline commands have nothing to log)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *in == "" || *csvPath == "" {
		return fmt.Errorf("import needs -i <image.json> and -csv <channels.csv>")
	}
	im, err := loadImage(*in)
	if err != nil {
		return err
	}
	fh, err := os.Open(*csvPath)
	if err != nil {
		return err
	}
	defer fh.Close()
	n, err := im.ImportCSV(fh)
	if err != nil {
		return err
	}
	if *out == "" {
		*out = *in
	}
	data, err := im.MarshalFile()
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("Merged %d channels from %s into %s\n", n, *csvPath, *out)
	return nil
}

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	in := fs.String("i", "", "input image file (JSON)")
	fs.StringVar(in, "in", "", "input image file (JSON)")
	verboseFlag(fs, "accepted for consistency (offline commands have nothing to log)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *in == "" {
		return fmt.Errorf("show needs -i <image.json>")
	}
	im, err := loadImage(*in)
	if err != nil {
		return err
	}
	fmt.Printf("Model: %s\n", im.Ident)
	fmt.Printf("%-4s %-12s %-10s %-10s %-7s %-7s %-5s\n", "Ch", "Name", "RX", "TX", "RXTone", "TXTone", "Pwr")
	for n := 1; n <= radio.ChannelCount; n++ {
		c := im.GetChannel(n)
		if c.Empty {
			continue
		}
		fmt.Printf("%-4d %-12s %-10s %-10s %-7s %-7s %-5s\n",
			c.Number, c.Name, c.RXFreq, c.TXFreq, c.RXTone, c.TXTone, c.Power)
	}
	return nil
}

func cmdDiff(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: uv28prog diff <imageA.json> <imageB.json>")
	}
	a, err := loadImage(args[0])
	if err != nil {
		return err
	}
	b, err := loadImage(args[1])
	if err != nil {
		return err
	}
	names := map[int]string{0x0000: "channels", 0x8000: "VFO/live tuning", 0x9000: "settings", 0xA000: "DTMF/zones"}
	for _, r := range radio.Regions {
		var d int
		for i := r.Start; i < r.Start+r.Size; i++ {
			if a.Mem[i] != b.Mem[i] {
				d++
			}
		}
		status := "identical"
		if d > 0 {
			status = fmt.Sprintf("%d bytes differ", d)
		}
		fmt.Printf("0x%04X %-16s %s\n", r.Start, names[r.Start], status)
	}
	// Channel-level differences.
	var chDiffs []int
	for n := 1; n <= radio.ChannelCount; n++ {
		if a.GetChannel(n) != b.GetChannel(n) {
			chDiffs = append(chDiffs, n)
		}
	}
	if len(chDiffs) == 0 {
		fmt.Println("All channels identical.")
	} else {
		fmt.Printf("Channels that differ: %v\n", chDiffs)
	}
	return nil
}

func loadImage(path string) (*radio.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return radio.UnmarshalFile(data)
}

func countChannels(im *radio.Image) int {
	n := 0
	for i := 1; i <= radio.ChannelCount; i++ {
		if !im.GetChannel(i).Empty {
			n++
		}
	}
	return n
}

// channelBlocks returns the block addresses that hold channel records
// (the first region), for channel-only writes.
func channelBlocks() []int {
	var out []int
	for _, a := range radio.Blocks() {
		if a < 0x7D00 {
			out = append(out, a)
		}
	}
	return out
}
