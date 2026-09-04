package random

import (
	"crypto/rand"
	"math/big"
)

var letterRunes = []rune("abcdefghijklmnopqrstuvwxyz")

// String creates a new random string with the given length.
func String(length int) string {
	b := make([]rune, length)
	for i := range b {
		b[i] = letterRunes[randInt(len(letterRunes))]
	}
	return string(b)
}

func InRange(min, max int) int {
	if max <= min {
		return min
	}
	return randInt(max-min) + min
}

// randInt returns a cryptographically secure random int in [0, max).
func randInt(max int) int {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return int(n.Int64())
}
