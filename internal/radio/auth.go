package radio

// The radio's Bluetooth LE bridge module answers a 20-byte challenge written
// to its control characteristic (UUID ...ff31) with one of two 20-byte
// responses. The vendor app only proceeds once it sees a valid response.
// Byte 4 of the challenge tells the module which radio family is attached;
// the UV-28 Pro/Plus family uses 0x02.

const challengeFamilyByte = 0x02

// NewChallenge builds a module challenge like the app's generateSerialNum().
func NewChallenge(r RandSource) []byte {
	c := make([]byte, 20)
	for i := 0; i < 5; i++ {
		c[i] = 0x3F
	}
	c[4] = challengeFamilyByte
	for i := 5; i < 20; i++ {
		c[i] = byte(r.Intn(99)) // (byte)(Math.random()*99.0)
	}
	return c
}

// AuthResponses returns the two responses the module may legitimately send
// for challenge c (generateSecretKey and generateSecretKey_new).
func AuthResponses(c []byte) (a, b []byte) {
	if len(c) < 20 {
		return nil, nil
	}
	// Java bytes are signed; replicate that exactly.
	sb := func(i int) int { return int(int8(c[i])) }
	jmod := func(x, y int) int { // Java % (truncated)
		if y == 0 {
			return 0
		}
		return x % y
	}
	a = make([]byte, 20)
	b = make([]byte, 20)
	for i := 0; i < 20; i++ {
		if i < 5 {
			a[i] = 0x21
			b[i] = 0x21
			continue
		}
		s := 0
		for j := 0; j <= i; j++ {
			s += sb(j)
		}
		a[i] = byte(int8(jmod(s, sb(19-(i-5))+(i-4))))

		s2 := 0
		for j := 0; j < 6; j++ {
			s2 += sb((24 - i) - j)
		}
		b[i] = byte(int8(jmod(s2, sb(24-i)+(25-i))))
	}
	return a, b
}
