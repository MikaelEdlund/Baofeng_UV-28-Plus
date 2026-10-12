package radio

import (
	"fmt"
	"strconv"
	"strings"
)

// dcsCodes are the 105 DCS codes, in the order the radio indexes them
// (InterphoneUtil.simulateMute).
var dcsCodes = []string{
	"023", "025", "026", "031", "032", "036", "043", "047", "051", "053", "054",
	"065", "071", "072", "073", "074", "114", "115", "116", "122", "125", "131",
	"132", "134", "143", "145", "152", "155", "156", "162", "165", "172", "174",
	"205", "212", "223", "225", "226", "243", "244", "245", "246", "251", "252",
	"255", "261", "263", "265", "266", "271", "274", "306", "311", "315", "325",
	"331", "332", "343", "346", "351", "356", "364", "365", "371", "411", "412",
	"413", "423", "431", "432", "445", "446", "452", "454", "455", "462", "464",
	"465", "466", "503", "506", "516", "523", "526", "532", "546", "565", "606",
	"612", "624", "627", "631", "632", "645", "654", "662", "664", "703", "712",
	"723", "731", "732", "734", "743", "754",
}

// Tone is a channel sub-audible tone: "OFF", a CTCSS value like "88.5", or a
// DCS code like "D023N" / "D023I".
type Tone string

const ToneOff Tone = "OFF"

// decodeTone converts the two stored bytes (lo, hi) into a Tone.
// Mirrors InterphoneUtil.parseHexMute.
func decodeTone(lo, hi byte) Tone {
	v := int(hi)<<8 | int(lo)
	switch {
	case v >= 1 && v <= 105:
		return Tone("D" + dcsCodes[v-1] + "N")
	case v >= 106 && v <= 210:
		return Tone("D" + dcsCodes[v-106] + "I")
	case v >= 600 && v <= 2599:
		// CTCSS in units of 0.1 Hz, insert decimal before last digit.
		s := strconv.Itoa(v)
		return Tone(s[:len(s)-1] + "." + s[len(s)-1:])
	default:
		return ToneOff
	}
}

// encodeTone converts a Tone back into the two stored bytes (lo, hi).
// "OFF" on a programmed channel is stored as 0x0000, matching the vendor
// app's channel editor (typing "OFF" writes 00 00). 0xFFFF is NOT used for
// "no tone" on an active channel: the radio treats it as a bogus tone and
// transmits a continuous audible tone. 0xFFFF only marks a fully empty
// channel, which SetChannel handles separately.
func encodeTone(t Tone) (lo, hi byte, err error) {
	s := strings.ToUpper(strings.TrimSpace(string(t)))
	if s == "" || s == "OFF" {
		return 0x00, 0x00, nil
	}
	if strings.HasPrefix(s, "D") && (strings.HasSuffix(s, "N") || strings.HasSuffix(s, "I")) {
		inverted := strings.HasSuffix(s, "I")
		code := s[1 : len(s)-1]
		idx := -1
		for i, c := range dcsCodes {
			if c == code {
				idx = i
				break
			}
		}
		if idx < 0 {
			return 0, 0, fmt.Errorf("unknown DCS code %q", t)
		}
		v := idx + 1
		if inverted {
			v = idx + 106
		}
		return byte(v), byte(v >> 8), nil
	}
	// CTCSS
	f, perr := strconv.ParseFloat(s, 64)
	if perr != nil {
		return 0, 0, fmt.Errorf("invalid tone %q", t)
	}
	v := int(f*10 + 0.5)
	return byte(v), byte(v >> 8), nil
}
