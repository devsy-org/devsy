package mcp

import (
	"context"
	"fmt"
)

// opSemaphore bounds how many expensive workspace operations run
// concurrently in one MCP server process.
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
	select {
	case s.slots <- struct{}{}:
		return func() { <-s.slots }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for a free operation slot: %w", ctx.Err())
	}
}
