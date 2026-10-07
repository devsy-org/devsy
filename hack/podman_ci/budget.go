package main

import (
	"fmt"
	"time"
)

type Clock interface {
	Now() time.Time
	Sleep(time.Duration)
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) Sleep(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
}

type Budget struct {
	timeout time.Duration
	started time.Time
	clock   Clock
}

func NewBudget(timeout time.Duration, clock Clock) (*Budget, error) {
	if timeout <= 0 {
		return nil, fmt.Errorf("bootstrap timeout must be positive")
	}
	if clock == nil {
		clock = realClock{}
	}
	return &Budget{timeout: timeout, started: clock.Now(), clock: clock}, nil
}

func (b *Budget) Remaining(reserve time.Duration) time.Duration {
	left := b.timeout - b.clock.Now().Sub(b.started) - reserve
	if left < 0 {
		return 0
	}
	return left
}

func (b *Budget) Bound(requested, reserve time.Duration) time.Duration {
	left := b.Remaining(reserve)
	if requested < left {
		return requested
	}
	return left
}
