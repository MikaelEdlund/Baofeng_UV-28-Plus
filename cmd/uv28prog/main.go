// Command uv28prog programs a Baofeng UV-28 Plus / UV-28 Pro (and the
// closely related UV-21 Pro / UV-32 Pro / AR-152 Pro) over Bluetooth LE,
// from Windows, without the vendor's Android app.
//
// Offline commands (export/import/show) work on any platform; the Bluetooth
// commands (scan/read/write) are built into the Windows binary.
package main

import (
	"fmt"
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

TYPICAL WORKFLOW
  1. uv28prog read  -o mine.json            (back up the radio first!)
  2. uv28prog export -i mine.json -o ch.csv
  3. edit ch.csv in a spreadsheet
  4. uv28prog import -i mine.json -csv ch.csv -o new.json
  5. uv28prog write  -i new.json            (keeps an automatic backup)

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
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

type flags struct {
	in, out, csv, addr, backup string
	timeout                    time.Duration
	verbose, all, yes          bool
	onlyChannels, noBackup     bool
	full                       bool
}

func parse(args []string) (*flags, error) {
	f := &flags{timeout: 20 * time.Second}
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("flag %s needs a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch a {
		case "-i", "-in":
			f.in, err = next()
		case "-o", "-out":
			f.out, err = next()
		case "-csv":
			f.csv, err = next()
		case "-addr":
			f.addr, err = next()
		case "-backup":
			f.backup, err = next()
		case "-timeout":
			var s string
			if s, err = next(); err == nil {
				f.timeout, err = time.ParseDuration(s)
			}
		case "-v", "-verbose":
			f.verbose = true
		case "-all":
			f.all = true
		case "-yes", "-y":
			f.yes = true
		case "-only-channels":
			f.onlyChannels = true
		case "-full":
			f.full = true
		case "-no-backup":
			f.noBackup = true
		default:
			return nil, fmt.Errorf("unknown flag %q", a)
		}
		if err != nil {
			return nil, err
		}
	}
	return f, nil
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
	f, err := parse(args)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Scanning for radios (turn the radio on, keep it close)...")
	found, err := Scan(f.timeout, f.addr, logger(f.verbose))
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
	f, err := parse(args)
	if err != nil {
		return err
	}
	out := f.out
	if out == "" {
		out = fmt.Sprintf("uv28-%s.json", time.Now().Format("20060102-150405"))
	}
	tr, err := Connect(f.addr, f.timeout, logger(f.verbose))
	if err != nil {
		return err
	}
	defer tr.Close()
	d := radio.NewDriver(tr, logger(f.verbose))
	fmt.Fprintln(os.Stderr, "Reading radio...")
	im, err := d.ReadImage(progressBar("read"))
	if err != nil {
		return err
	}
	data, err := im.MarshalFile()
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	n := countChannels(im)
	fmt.Printf("Read OK. Model %q, %d channels. Saved to %s\n", im.Ident, n, out)
	return nil
}

func cmdWrite(args []string) error {
	f, err := parse(args)
	if err != nil {
		return err
	}
	if f.in == "" {
		return fmt.Errorf("write needs -i <image.json>")
	}
	im, err := loadImage(f.in)
	if err != nil {
		return err
	}
	if !f.yes {
		return fmt.Errorf("writing to the radio changes it. Re-run with -yes to confirm.\n" +
			"  (a full backup of the current radio is saved automatically first)")
	}

	// The radio expects one operation per Bluetooth session, so the backup
	// read and the write each use their own fresh connection (just like
	// running two separate commands).
	if !f.noBackup {
		bpath := f.backup
		if bpath == "" {
			bpath = fmt.Sprintf("uv28-backup-%s.json", time.Now().Format("20060102-150405"))
		}
		fmt.Fprintln(os.Stderr, "Backing up current radio contents first...")
		tr, err := Connect(f.addr, f.timeout, logger(f.verbose))
		if err != nil {
			return err
		}
		cur, err := radio.NewDriver(tr, logger(f.verbose)).ReadImage(progressBar("backup"))
		tr.Close()
		if err != nil {
			return fmt.Errorf("backup read failed, aborting before any write: %w", err)
		}
		bdata, _ := cur.MarshalFile()
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
	if f.full {
		only = nil
		scope = "all regions"
	}
	fmt.Fprintf(os.Stderr, "Writing to radio (%s)...\n", scope)
	tr, err := Connect(f.addr, f.timeout, logger(f.verbose))
	if err != nil {
		return err
	}
	defer tr.Close()
	if err := radio.NewDriver(tr, logger(f.verbose)).WriteImage(im, only, progressBar("write")); err != nil {
		return err
	}
	fmt.Println("Write OK.")
	return nil
}

func cmdExport(args []string) error {
	f, err := parse(args)
	if err != nil {
		return err
	}
	if f.in == "" {
		return fmt.Errorf("export needs -i <image.json>")
	}
	im, err := loadImage(f.in)
	if err != nil {
		return err
	}
	out := f.out
	if out == "" {
		out = strings.TrimSuffix(filepath.Base(f.in), filepath.Ext(f.in)) + ".csv"
	}
	fh, err := os.Create(out)
	if err != nil {
		return err
	}
	defer fh.Close()
	if err := im.ExportCSV(fh, f.all); err != nil {
		return err
	}
	fmt.Printf("Exported %d channels to %s\n", countChannels(im), out)
	return nil
}

func cmdImport(args []string) error {
	f, err := parse(args)
	if err != nil {
		return err
	}
	if f.in == "" || f.csv == "" {
		return fmt.Errorf("import needs -i <image.json> and -csv <channels.csv>")
	}
	im, err := loadImage(f.in)
	if err != nil {
		return err
	}
	fh, err := os.Open(f.csv)
	if err != nil {
		return err
	}
	defer fh.Close()
	n, err := im.ImportCSV(fh)
	if err != nil {
		return err
	}
	out := f.out
	if out == "" {
		out = f.in
	}
	data, err := im.MarshalFile()
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return err
	}
	fmt.Printf("Merged %d channels from %s into %s\n", n, f.csv, out)
	return nil
}

func cmdShow(args []string) error {
	f, err := parse(args)
	if err != nil {
		return err
	}
	if f.in == "" {
		return fmt.Errorf("show needs -i <image.json>")
	}
	im, err := loadImage(f.in)
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
// (the first region), for -only-channels writes.
func channelBlocks() []int {
	var out []int
	for _, a := range radio.Blocks() {
		if a < 0x7D00 {
			out = append(out, a)
		}
	}
	return out
}
