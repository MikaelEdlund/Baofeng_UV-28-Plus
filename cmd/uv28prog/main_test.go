package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"uv28prog/internal/radio"
)

func TestParseFlags(t *testing.T) {
	newFS := func() (*flag.FlagSet, *string) {
		fs := flag.NewFlagSet("demo", flag.ContinueOnError)
		return fs, fs.String("o", "", "output")
	}

	fs, out := newFS()
	if err := parseFlags(fs, []string{"-o", "x.csv"}); err != nil || *out != "x.csv" {
		t.Fatalf("plain flags: err=%v out=%q", err, *out)
	}

	fs, _ = newFS()
	err := parseFlags(fs, []string{"x.csv"})
	if err == nil || !strings.Contains(err.Error(), `unexpected argument "x.csv"`) {
		t.Fatalf("stray argument: %v", err)
	}

	// The flag package stops at the first positional argument, so a flag
	// after it must not be silently dropped either.
	fs, _ = newFS()
	if err := parseFlags(fs, []string{"stray", "-o", "x.csv"}); err == nil {
		t.Fatal("stray argument followed by a flag was accepted")
	}

	fs, _ = newFS()
	if err := parseFlags(fs, []string{"-nope"}); err == nil || !strings.Contains(err.Error(), "-nope") {
		t.Fatalf("unknown flag: %v", err)
	}

	fs, _ = newFS()
	if err := parseFlags(fs, []string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("-h should return flag.ErrHelp, got %v", err)
	}
}

func writeTestImage(t *testing.T, path string) []byte {
	t.Helper()
	im := radio.NewBlankImage()
	im.Ident = "UV28PRO"
	if err := im.SetChannel(radio.Channel{Number: 1, Name: "CALL", RXFreq: "145.50000", TXFreq: "145.50000", RXTone: "OFF", TXTone: "OFF", Power: "High", SigCode: 1, PTTID: "Off"}); err != nil {
		t.Fatal(err)
	}
	data, err := im.MarshalFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return data
}

// "import -i mine.json -csv ch.csv new.json" used to ignore new.json and
// overwrite mine.json (often the only copy of the radio as read).
func TestImportStrayArgumentLeavesInputUntouched(t *testing.T) {
	dir := t.TempDir()
	mine := filepath.Join(dir, "mine.json")
	orig := writeTestImage(t, mine)
	csvPath := filepath.Join(dir, "ch.csv")
	csv := "Channel,Name,RXFreq,TXFreq\n5,EDIT,146.52000,146.52000\n"
	if err := os.WriteFile(csvPath, []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(dir, "new.json")

	if err := cmdImport([]string{"-i", mine, "-csv", csvPath, newPath}); err == nil {
		t.Fatal("import accepted a stray argument")
	}
	if got, _ := os.ReadFile(mine); !bytes.Equal(got, orig) {
		t.Fatal("input image was modified")
	}
	if _, err := os.Stat(newPath); err == nil {
		t.Fatal("output file was created")
	}

	// The same import with -o works and still leaves the input alone.
	if err := cmdImport([]string{"-i", mine, "-csv", csvPath, "-o", newPath}); err != nil {
		t.Fatalf("import with -o: %v", err)
	}
	if got, _ := os.ReadFile(mine); !bytes.Equal(got, orig) {
		t.Fatal("input image was modified by import -o")
	}
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	im, err := radio.UnmarshalFile(data)
	if err != nil {
		t.Fatal(err)
	}
	if c := im.GetChannel(5); c.Name != "EDIT" || c.RXFreq != "146.52000" {
		t.Fatalf("channel 5 not merged: %+v", c)
	}
}

func TestExportStrayArgument(t *testing.T) {
	dir := t.TempDir()
	mine := filepath.Join(dir, "mine.json")
	writeTestImage(t, mine)
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	if err := cmdExport([]string{"-i", mine, "edited.csv"}); err == nil {
		t.Fatal("export accepted a stray argument")
	}
	if _, err := os.Stat(filepath.Join(dir, "mine.csv")); err == nil {
		t.Fatal("export wrote mine.csv despite the error")
	}
}
