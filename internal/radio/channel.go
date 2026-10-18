package radio

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// ChannelCount is the number of programmable channels on this radio.
const ChannelCount = 1000

// recordSize is the size of one channel record (two 16-byte halves).
const recordSize = 32

// Channel is one memory channel in a friendly, editable form.
type Channel struct {
	Number   int    // 1..ChannelCount
	Empty    bool   // true when the channel is unprogrammed
	Name     string // up to 12 bytes (GBK), usually ASCII
	RXFreq   string // MHz, e.g. "145.50000"; "" when empty
	TXFreq   string // MHz; "" means transmit disabled (RX only)
	RXTone   Tone   // receive sub-audible tone / squelch
	TXTone   Tone   // transmit sub-audible tone
	Power    string // "High" | "Mid" | "Low"
	Narrow   bool   // app "W/N" switch (bit6 of flags)
	Scan     bool   // add to scan list (bit2)
	BusyLock bool   // busy-channel lockout (bit3)
	FreqHop  bool   // frequency hopping (bit0)
	SigCode  int    // signal code 1..20
	PTTID    string // "Off" | "BOT" | "EOT" | "Both"
}

var gbkDecoder = simplifiedchinese.GBK.NewDecoder()
var gbkEncoder = simplifiedchinese.GBK.NewEncoder()

// bcdFreqDecode turns 4 BCD bytes (little-endian pairs) into a MHz string.
func bcdFreqDecode(b []byte) (string, bool) {
	if b[0] == 0xFF {
		return "", false
	}
	// Each byte is two decimal digits; order high->low is b[3] b[2] b[1] b[0].
	digits := fmt.Sprintf("%02X%02X%02X%02X", b[3], b[2], b[1], b[0])
	for _, c := range digits {
		if c < '0' || c > '9' {
			return "", false // not valid BCD -> treat as unset
		}
	}
	// digits is 8 chars: 3 integer + 5 fractional MHz.
	return digits[:3] + "." + digits[3:], true
}

// bcdFreqEncode turns a MHz string into 4 BCD bytes. Empty -> all 0xFF.
// Uses pure integer/decimal parsing to avoid floating-point rounding issues.
func bcdFreqEncode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return []byte{0xFF, 0xFF, 0xFF, 0xFF}, nil
	}
	// Accept optional leading zeros and a single decimal point.
	parts := strings.Split(s, ".")
	if len(parts) > 2 {
		return nil, fmt.Errorf("invalid frequency %q", s)
	}
	intPart := parts[0]
	fracPart := ""
	if len(parts) == 2 {
		fracPart = parts[1]
	}
	if intPart == "" {
		intPart = "0"
	}
	// Pad fractional part to 5 digits (10 Hz resolution).
	for len(fracPart) < 5 {
		fracPart += "0"
	}
	if len(fracPart) > 5 {
		fracPart = fracPart[:5] // truncate extra precision
	}
	digits := intPart + fracPart
	// Ensure exactly 8 digits by padding or rejecting.
	if len(digits) > 8 {
		return nil, fmt.Errorf("frequency out of range: %q", s)
	}
	for len(digits) < 8 {
		digits = "0" + digits
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return nil, fmt.Errorf("invalid frequency %q", s)
		}
	}
	v, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || v < 0 || v > 99999999 {
		return nil, fmt.Errorf("frequency out of range: %q", s)
	}
	bcd := func(two string) byte {
		hi := byte(two[0] - '0')
		lo := byte(two[1] - '0')
		return hi<<4 | lo
	}
	return []byte{
		bcd(digits[6:8]),
		bcd(digits[4:6]),
		bcd(digits[2:4]),
		bcd(digits[0:2]),
	}, nil
}

// checkFreqRange performs a conservative frequency-range check.
// Typical dual-band amateur radios in this family cover roughly
// 136–174 MHz (VHF) and 400–480 MHz (UHF). We accept a slightly wider
// common subset and reject clearly invalid values.
func checkFreqRange(model, freq string) error {
	if freq == "" {
		return nil
	}
	f, err := strconv.ParseFloat(freq, 64)
	if err != nil {
		return fmt.Errorf("invalid frequency %q", freq)
	}
	// Conservative common subset for the UV-28 family.
	// Allow a little margin for different advertised limits.
	ok := (f >= 136.0 && f <= 174.0) || (f >= 400.0 && f <= 520.0)
	if !ok {
		return fmt.Errorf("frequency %s MHz is outside the conservative band limits for model %q (VHF 136-174 / UHF 400-520)", freq, model)
	}
	return nil
}

func decodeName(raw []byte) string {
	// Trim trailing 0xFF / 0x00.
	end := len(raw)
	for end > 0 && (raw[end-1] == 0xFF || raw[end-1] == 0x00) {
		end--
	}
	if end == 0 {
		return ""
	}
	out, err := gbkDecoder.Bytes(raw[:end])
	if err != nil {
		return strings.TrimRight(string(raw[:end]), "\x00\xff")
	}
	return string(out)
}

func encodeName(s string) ([]byte, error) {
	out := make([]byte, 12)
	for i := range out {
		out[i] = 0xFF
	}
	if s == "" {
		return out, nil
	}
	enc, err := gbkEncoder.Bytes([]byte(s))
	if err != nil {
		return nil, fmt.Errorf("name %q not encodable: %w", s, err)
	}
	if len(enc) > 12 {
		return nil, fmt.Errorf("name %q too long (max 12 bytes)", s)
	}
	copy(out, enc)
	return out, nil
}

var powerNames = map[byte]string{0x00: "High", 0x02: "Mid"}
var pttidNames = map[byte]string{0x01: "BOT", 0x02: "EOT", 0x03: "Both"}

// GetChannel decodes channel n (1-based) from the image.
func (im *Image) GetChannel(n int) Channel {
	base := (n - 1) * recordSize
	c := Channel{Number: n}
	fr := im.Mem[base : base+16]
	name := im.Mem[base+16+4 : base+16+16]

	if fr[0] == 0xFF {
		c.Empty = true
		return c
	}
	c.RXFreq, _ = bcdFreqDecode(fr[0:4])
	if tx, ok := bcdFreqDecode(fr[4:8]); ok {
		c.TXFreq = tx
	}
	c.RXTone = decodeTone(fr[8], fr[9])
	c.TXTone = decodeTone(fr[10], fr[11])
	if p, ok := powerNames[fr[14]]; ok {
		c.Power = p
	} else {
		c.Power = "Low"
	}
	flags := fr[15]
	c.FreqHop = flags&(1<<0) != 0
	c.Scan = flags&(1<<2) != 0
	c.BusyLock = flags&(1<<3) != 0
	c.Narrow = flags&(1<<6) != 0
	sc := int(fr[12])
	if sc <= 19 {
		c.SigCode = sc + 1
	} else {
		c.SigCode = 1
	}
	if p, ok := pttidNames[fr[13]]; ok {
		c.PTTID = p
	} else {
		c.PTTID = "Off"
	}
	c.Name = decodeName(name)
	return c
}

// SetChannel encodes a channel back into the image. Bytes not represented by
// the Channel struct are preserved from the existing record.
func (im *Image) SetChannel(c Channel) error {
	if c.Number < 1 || c.Number > ChannelCount {
		return fmt.Errorf("channel number %d out of range", c.Number)
	}
	base := (c.Number - 1) * recordSize
	fr := im.Mem[base : base+16]
	nameRec := im.Mem[base+16 : base+32]

	if c.Empty || strings.TrimSpace(c.RXFreq) == "" {
		for i := range fr {
			fr[i] = 0xFF
		}
		for i := 0; i < 16; i++ {
			nameRec[i] = 0xFF
		}
		return nil
	}

	rx, err := bcdFreqEncode(c.RXFreq)
	if err != nil {
		return fmt.Errorf("ch %d rx: %w", c.Number, err)
	}
	if err := checkFreqRange(im.Ident, c.RXFreq); err != nil {
		return fmt.Errorf("ch %d rx: %w", c.Number, err)
	}
	copy(fr[0:4], rx)
	tx, err := bcdFreqEncode(c.TXFreq)
	if err != nil {
		return fmt.Errorf("ch %d tx: %w", c.Number, err)
	}
	if c.TXFreq != "" {
		if err := checkFreqRange(im.Ident, c.TXFreq); err != nil {
			return fmt.Errorf("ch %d tx: %w", c.Number, err)
		}
	}
	copy(fr[4:8], tx)

	rlo, rhi, err := encodeTone(c.RXTone)
	if err != nil {
		return fmt.Errorf("ch %d rxtone: %w", c.Number, err)
	}
	fr[8], fr[9] = rlo, rhi
	tlo, thi, err := encodeTone(c.TXTone)
	if err != nil {
		return fmt.Errorf("ch %d txtone: %w", c.Number, err)
	}
	fr[10], fr[11] = tlo, thi

	sc := c.SigCode
	if sc < 1 || sc > 20 {
		sc = 1
	}
	fr[12] = byte(sc - 1)

	fr[13] = 0x00
	for b, name := range pttidNames {
		if strings.EqualFold(name, c.PTTID) {
			fr[13] = b
		}
	}

	switch strings.ToLower(c.Power) {
	case "high":
		fr[14] = 0x00
	case "mid", "middle", "medium":
		fr[14] = 0x02
	default:
		fr[14] = 0x01
	}

	var flags byte
	if c.FreqHop {
		flags |= 1 << 0
	}
	if c.Scan {
		flags |= 1 << 2
	}
	if c.BusyLock {
		flags |= 1 << 3
	}
	if c.Narrow {
		flags |= 1 << 6
	}
	// preserve mute-way bit (bit4) and any unknown bits from the old value
	flags |= fr[15] & (1 << 4)
	fr[15] = flags

	nm, err := encodeName(c.Name)
	if err != nil {
		return fmt.Errorf("ch %d: %w", c.Number, err)
	}
	copy(nameRec[4:16], nm)
	return nil
}
