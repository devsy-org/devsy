package mcp

import (
	"context"
	"fmt"
)

type opSemaphore struct {
	slots chan struct{}
}

func newOpSemaphore(max int) *opSemaphore {
	if max <= 0 {
		max = 1
	}
	return &opSemaphore{slots: make(chan struct{}, max)}
}

func (s *opSemaphore) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("waiting for a free operation slot: %w", err)
	}
	select {
	case s.slots <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-s.slots
			return nil, fmt.Errorf("waiting for a free operation slot: %w", err)
		}
		return func() { <-s.slots }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for a free operation slot: %w", ctx.Err())
	}
}
