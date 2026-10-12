package radio

import (
	"bytes"
	"testing"
)

func TestCryptRoundTripAndPassthrough(t *testing.T) {
	in := []byte{0x00, 0xFF, 0x12, 0x34, 'C', 'O', 0x37, 0xAB, 0x55}
	enc := Crypt(1, in)
	dec := Crypt(1, enc)
	if !bytes.Equal(in, dec) {
		t.Fatalf("crypt not involutive: %X -> %X -> %X", in, enc, dec)
	}
	// 0x00, 0xFF, key byte and complement pass through unchanged.
	if enc[0] != 0x00 || enc[1] != 0xFF {
		t.Fatalf("0x00/0xFF not passed through: %X", enc)
	}
}

func TestFreqRoundTrip(t *testing.T) {
	cases := []string{"145.50000", "446.00625", "108.00000", "520.00000", "027.12500"}
	for _, f := range cases {
		b, err := bcdFreqEncode(f)
		if err != nil {
			t.Fatalf("encode %s: %v", f, err)
		}
		got, ok := bcdFreqDecode(b)
		if !ok || got != f {
			t.Fatalf("freq round trip %s -> %X -> %q", f, b, got)
		}
	}
	// 0xFF means unset.
	if _, ok := bcdFreqDecode([]byte{0xFF, 0xFF, 0xFF, 0xFF}); ok {
		t.Fatal("0xFF freq should be unset")
	}
}

func TestToneRoundTrip(t *testing.T) {
	cases := []Tone{"OFF", "88.5", "123.0", "250.3", "D023N", "D754I", "D023I"}
	for _, tn := range cases {
		lo, hi, err := encodeTone(tn)
		if err != nil {
			t.Fatalf("encode %s: %v", tn, err)
		}
		got := decodeTone(lo, hi)
		if got != tn {
			t.Fatalf("tone round trip %s -> %02X%02X -> %s", tn, hi, lo, got)
		}
	}
}

func TestChannelRoundTrip(t *testing.T) {
	im := NewBlankImage()
	c := Channel{
		Number: 7, Name: "REPEATER", RXFreq: "145.50000", TXFreq: "145.60000",
		RXTone: "88.5", TXTone: "D023N", Power: "Mid", Narrow: true, Scan: true,
		BusyLock: false, FreqHop: false, SigCode: 3, PTTID: "BOT",
	}
	if err := im.SetChannel(c); err != nil {
		t.Fatal(err)
	}
	got := im.GetChannel(7)
	if got.Name != c.Name || got.RXFreq != c.RXFreq || got.TXFreq != c.TXFreq ||
		got.RXTone != c.RXTone || got.TXTone != c.TXTone || got.Power != c.Power ||
		got.Narrow != c.Narrow || got.Scan != c.Scan || got.SigCode != c.SigCode ||
		got.PTTID != c.PTTID {
		t.Fatalf("channel round trip mismatch:\n in: %+v\nout: %+v", c, got)
	}
	if im.GetChannel(8).Empty != true {
		t.Fatal("channel 8 should be empty")
	}
}

func TestCSVRoundTrip(t *testing.T) {
	im := NewBlankImage()
	chs := []Channel{
		{Number: 1, Name: "CALL", RXFreq: "145.50000", TXFreq: "145.50000", RXTone: "OFF", TXTone: "OFF", Power: "High", Scan: true, SigCode: 1, PTTID: "Off"},
		{Number: 2, Name: "RPT2", RXFreq: "438.80000", TXFreq: "431.20000", RXTone: "D023N", TXTone: "67.0", Power: "Low", Narrow: true, SigCode: 2, PTTID: "Both"},
	}
	for _, c := range chs {
		if err := im.SetChannel(c); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := im.ExportCSV(&buf, false); err != nil {
		t.Fatal(err)
	}
	im2 := NewBlankImage()
	n, err := im2.ImportCSV(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 channels imported, got %d", n)
	}
	if !bytes.Equal(im.Mem[:], im2.Mem[:]) {
		t.Fatal("CSV round trip changed the image bytes")
	}
}

func TestImageFileRoundTrip(t *testing.T) {
	im := NewBlankImage()
	im.Ident = "UV28PRO"
	_ = im.SetChannel(Channel{Number: 1, Name: "A", RXFreq: "145.00000", TXFreq: "145.00000", RXTone: "OFF", TXTone: "OFF", Power: "High", SigCode: 1, PTTID: "Off"})
	data, err := im.MarshalFile()
	if err != nil {
		t.Fatal(err)
	}
	im2, err := UnmarshalFile(data)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(im.Mem[:], im2.Mem[:]) || im2.Ident != "UV28PRO" {
		t.Fatal("image file round trip mismatch")
	}
}
