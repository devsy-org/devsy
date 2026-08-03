package mcp

import (
	"context"
	"fmt"
)

// opSemaphore bounds how many expensive workspace operations (exec, create,
// start) run concurrently in one MCP server process, so an orchestrator
// driving many workspaces can't overwhelm the local Docker/Kubernetes
// backend. Devsy itself has no other admission control (see pkg/provider
// locks, which serialize per-workspace but not process-wide).
type opSemaphore struct {
	slots chan struct{}
}

func newOpSemaphore(max int) *opSemaphore {
	if max <= 0 {
		max = 1
	}
	return &opSemaphore{slots: make(chan struct{}, max)}
}

// acquire blocks until a slot is free or ctx is done. On success the caller
// must call the returned release func exactly once.
func (s *opSemaphore) acquire(ctx context.Context) (func(), error) {
	select {
	case s.slots <- struct{}{}:
		return func() { <-s.slots }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("waiting for a free operation slot: %w", ctx.Err())
	}
}
