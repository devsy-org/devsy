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
		b[i] = letterRunes[intn(len(letterRunes))]
	}
	return string(b)
}

func InRange(min, max int) int {
	if max <= min {
		return min
	}
	return intn(max-min) + min
}

// intn returns a uniform value in [0, n). rand.Int never fails with the
// default reader since Go 1.24.
func intn(n int) int {
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		panic(err)
	}
	return int(v.Int64())
}
