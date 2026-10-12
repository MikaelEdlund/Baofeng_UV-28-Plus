// Package radio implements the Baofeng UV-28 Plus / UV-28 Pro (and siblings
// UV-21 Pro, UV-32 Pro, AR-152 Pro) programming protocol as used by the
// vendor's Android app over the radio's built-in Bluetooth LE module.
package radio

// cryptTable is the XOR key table used by the radio for block payloads.
// One 4-byte row is selected per session by the "SEND" key packet.
var cryptTable = [20][4]byte{
	{'B', 'H', 'T', ' '}, {'C', 'O', ' ', '7'}, {'A', ' ', 'E', 'S'}, {' ', 'E', 'I', 'Y'},
	{'M', ' ', 'P', 'Q'}, {'X', 'N', ' ', 'Y'}, {'R', 'V', 'B', ' '}, {' ', 'H', 'Q', 'P'},
	{'W', ' ', 'R', 'C'}, {'M', 'S', ' ', 'N'}, {' ', 'S', 'A', 'T'}, {'K', ' ', 'D', 'H'},
	{'Z', 'O', ' ', 'R'}, {'C', ' ', 'S', 'L'}, {'6', 'R', 'B', ' '}, {' ', 'J', 'C', 'G'},
	{'P', 'N', ' ', 'V'}, {'J', ' ', 'P', 'K'}, {'E', 'K', ' ', 'L'}, {'I', ' ', 'L', 'Z'},
}

// Crypt encrypts or decrypts (the operation is its own inverse) a block
// payload with key row idx. Bytes 0x00, 0xFF, the key byte and its
// complement are passed through unchanged, as are positions where the key
// byte is a space.
func Crypt(idx int, in []byte) []byte {
	out := make([]byte, len(in))
	if idx < 0 || idx >= len(cryptTable) {
		copy(out, in)
		return out
	}
	key := cryptTable[idx]
	for i, b := range in {
		k := key[i%4]
		if k == ' ' || b == 0x00 || b == 0xFF || b == k || b == k^0xFF {
			out[i] = b
		} else {
			out[i] = b ^ k
		}
	}
	return out
}

// KeyIndexFromSendPacket returns the crypt table row selected by a 25-byte
// "SEND" packet, mirroring the vendor app's logic.
func KeyIndexFromSendPacket(p []byte) int {
	if len(p) < 15 {
		return -1
	}
	n := int(p[4])
	var i2 int
	if n&0x20 == 0x20 {
		i2 = (n-0x20)*2 + 1
	} else {
		i2 = (n - 0x10) * 2
	}
	pos := i2 + 5
	if pos < 5 || pos >= len(p) {
		return -1
	}
	return int(p[pos])
}

// RandSource is the minimal randomness interface the protocol needs.
type RandSource interface {
	Intn(n int) int
}

// NewSendPacket builds the 25-byte "SEND" packet that selects the session's
// crypt key, exactly like the vendor app (handshakeCode_BFUV20).
func NewSendPacket(r RandSource) []byte {
	p := make([]byte, 25)
	copy(p, "SEND")
	p[4] = byte(((r.Intn(2) + 1) << 4) | r.Intn(5))
	for i := 0; i < 19; i++ {
		p[5+i] = byte(r.Intn(20))
	}
	return p
}
