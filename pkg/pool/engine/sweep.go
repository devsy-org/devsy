package engine

import (
	"context"
	"errors"
	"time"

	"github.com/devsy-org/devsy/pkg/pool/session"
)

// Sweep gives every reclaiming and orphaned session one reclaim attempt if it
// is due. It returns the earliest time another attempt falls due, or the zero
// time when none waits. An incomplete attempt, one not yet due and a session
// another caller already moved on from (a stale listing) are normal; an
// audit, store or context error stops the pass, and a context canceled during
// the pass is returned even when every attempt looked normal.
func (e *Engine) Sweep(ctx context.Context) (time.Time, error) {
	sessions, err := e.store.List()
	if err != nil {
		return time.Time{}, err
	}
	var next time.Time
	for _, s := range sessions {
		if s.State != session.StateReclaiming && s.State != session.StateOrphaned {
			continue
		}
		got, err := e.Reclaim(ctx, s.ID)
		switch {
		case err == nil, errors.Is(err, ErrNotReclaimable):
		case waiting(err):
			next = earliest(next, Due(got))
		default:
			return next, err
		}
	}
	return next, ctx.Err()
}

func waiting(err error) bool {
	return errors.Is(err, ErrNotDue) || errors.Is(err, ErrReclaimIncomplete)
}

func earliest(a, b time.Time) time.Time {
	if a.IsZero() || b.Before(a) {
		return b
	}
	return a
}
