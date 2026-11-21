package scryptcost

import (
	"sync/atomic"

	"filippo.io/age"
)

var override atomic.Int64

func Set(workFactor int) (restore func()) {
	previous := override.Swap(int64(workFactor))
	return func() { override.Store(previous) }
}

func Recipient(passphrase string) (*age.ScryptRecipient, error) {
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, err
	}
	if factor := override.Load(); factor > 0 {
		recipient.SetWorkFactor(int(factor))
	}
	return recipient, nil
}
