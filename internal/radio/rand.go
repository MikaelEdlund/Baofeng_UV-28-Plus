package radio

import (
	"crypto/rand"
	"math/big"
)

type cryptoRand struct{}

func (cryptoRand) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	max := big.NewInt(int64(n))
	v, err := rand.Int(rand.Reader, max)
	if err != nil {
		// Fallback extremely unlikely; return 0.
		return 0
	}
	return int(v.Int64())
}

func defaultRand() RandSource { return cryptoRand{} }
