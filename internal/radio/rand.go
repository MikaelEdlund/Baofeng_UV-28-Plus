package radio

import "math/rand"

type stdRand struct{}

func (stdRand) Intn(n int) int { return rand.Intn(n) }

func defaultRand() RandSource { return stdRand{} }
