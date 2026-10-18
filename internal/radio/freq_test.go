package radio

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseFreq(t *testing.T) {
	cases := []struct {
		in, want string // want "" means an error is expected
	}{
		{"145.5", "145.50000"},
		{"145.50000", "145.50000"},
		{" 145.5 ", "145.50000"},
		{"446.00625", "446.00625"},
		{"145.", "145.00000"},
		{".5", "000.50000"},
		{"050.00000", "050.00000"},  // how 50 MHz is exported
		{"0145.50000", "145.50000"}, // leading zeros are fine
		{"145.123456", "145.12346"}, // rounds to the nearest 10 Hz ...
		{"145.123454", "145.12345"},
		{"145.29999999999998", "145.30000"}, // ... so float noise is harmless
		{"999.999994", "999.99999"},
		{"999.999995", ""}, // rounds past the 8-digit field
		{"1000", ""},
		{"", ""},
		{".", ""},
		{"-145.5", ""},
		{"+145.5", ""},
		{"145.5.5", ""},
		{"145,5", ""},
		{"1.455e2", ""},
		{"abc", ""},
	}
	for _, c := range cases {
		v, err := parseFreq(c.in)
		if c.want == "" {
			if err == nil {
				t.Errorf("parseFreq(%q) = %d, want an error", c.in, v)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseFreq(%q): %v", c.in, err)
			continue
		}
		got, ok := bcdFreqDecode(freqToBCD(v))
		if !ok || got != c.want {
			t.Errorf("parseFreq(%q) -> %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSetChannelBands(t *testing.T) {
	ok := []struct{ rx, tx string }{
		{"118.10000", ""},          // airband, receive-only
		{"145.50000", "145.50000"}, // 2 m
		{"162.55000", ""},          // weather
		{"223.50000", "223.50000"}, // 1.25 m
		{"380.00000", ""},
		{"446.00625", "446.00625"}, // 70 cm / PMR
		{"510.00000", ""},
	}
	for _, c := range ok {
		im := NewBlankImage()
		if err := im.SetChannel(Channel{Number: 1, RXFreq: c.rx, TXFreq: c.tx, RXTone: "OFF", TXTone: "OFF"}); err != nil {
			t.Errorf("rx %s tx %q rejected: %v", c.rx, c.tx, err)
		}
	}

	bad := []struct{ rx, tx string }{
		{"14.55", ""},     // typo for 145.5
		{"90.00000", ""},  // FM broadcast, not a channel band
		{"395.00000", ""}, // gap between bands
		{"600.00000", ""},
		{"145.50000", "14.55"},  // bad TX only
		{"145.50000", "1455.0"}, // too many digits
	}
	for _, c := range bad {
		im := NewBlankImage()
		_ = im.SetChannel(Channel{Number: 1, Name: "KEEP", RXFreq: "433.50000", TXFreq: "433.50000", RXTone: "OFF", TXTone: "OFF"})
		before := im.Mem
		err := im.SetChannel(Channel{Number: 1, Name: "NEW", RXFreq: c.rx, TXFreq: c.tx, RXTone: "OFF", TXTone: "OFF"})
		if err == nil {
			t.Errorf("rx %s tx %q accepted, want an error", c.rx, c.tx)
			continue
		}
		if im.Mem != before {
			t.Errorf("rx %s tx %q: rejected channel still modified the image", c.rx, c.tx)
		}
	}
}

// An unedited export must import cleanly whatever bands the radio's channels
// use (this is the README workflow).
func TestCSVRoundTripAllBands(t *testing.T) {
	im := NewBlankImage()
	freqs := []struct{ rx, tx string }{
		{"118.10000", ""}, {"145.50000", "145.50000"}, {"223.50000", "223.50000"},
		{"380.00000", ""}, {"438.80000", "431.20000"}, {"510.00000", ""},
	}
	for i, f := range freqs {
		if err := im.SetChannel(Channel{Number: i + 1, Name: "CH", RXFreq: f.rx, TXFreq: f.tx, RXTone: "OFF", TXTone: "OFF", Power: "High", SigCode: 1, PTTID: "Off"}); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := im.ExportCSV(&buf, false); err != nil {
		t.Fatal(err)
	}
	im2 := NewBlankImage()
	n, err := im2.ImportCSV(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatalf("re-import of an unedited export failed: %v", err)
	}
	if n != len(freqs) {
		t.Fatalf("imported %d channels, want %d", n, len(freqs))
	}
	if !bytes.Equal(im.Mem[:], im2.Mem[:]) {
		t.Fatal("CSV round trip changed the image bytes")
	}
}
