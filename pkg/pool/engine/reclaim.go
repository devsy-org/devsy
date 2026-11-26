package engine

import (
	"context"
	"slices"
	"time"

	"github.com/devsy-org/devsy-signals/attrkey"
	"github.com/devsy-org/devsy-signals/envelope"
	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/session"
)

const (
	// MaxFailures is the number of failed attempts after which a reclaiming
	// session becomes orphaned. Attempts continue after that.
	MaxFailures = 5

	backoffBase = time.Second
	backoffMax  = 5 * time.Minute

	resultDeleted = "deleted"
	resultPartial = "partial"
	resultFailed  = "failed"

	kindContainer = "container"
)

// auditKinds maps provider resource kinds to the audit enum. A kind not listed
// is left out of devsy.reclaim.remaining rather than failing the audit write.
var auditKinds = map[string]string{
	"workspace":      kindContainer,
	"container":      kindContainer,
	"volume":         "volume",
	"network":        "network",
	"sidecar":        "sidecar",
	"network_policy": "network_policy",
	"service":        "service",
}

// Backoff is the wait after the nth attempt: 1s doubling to a cap of 5m.
func Backoff(attempts int) time.Duration {
	switch {
	case attempts <= 0:
		return 0
	case attempts > 9:
		return backoffMax
	default:
		return min(backoffBase<<(attempts-1), backoffMax)
	}
}

// Due is when the next reclaim attempt may run. The wait counts from when the
// last attempt count was stored, at the start of that attempt. The move to
// orphaned stores a newer time, at the end of the fifth attempt.
func Due(s session.Session) time.Time {
	return s.Updated.Add(Backoff(s.Attempts))
}

type outcome struct {
	result    string
	remaining []string
}

// Reclaim runs one attempt on a reclaiming or orphaned session that is due.
// It stores the attempt count before touching the provider, then removes the
// session's resources and trusts a fresh listing, not the removal error, to
// say what is left. It records session.reclaim before any state change. A
// session with no resources left becomes deleted. The MaxFailures-th failed
// attempt makes a reclaiming session orphaned. An attempt that leaves
// resources returns ErrReclaimIncomplete with the stored session. An attempt
// that is not due returns ErrNotDue with the session as loaded, even when the
// context is canceled. A context seen canceled before the attempt count is
// stored changes nothing. One seen canceled after the provider calls keeps the
// stored count, because it is written before the provider is touched, and
// writes no event or state change. Such an attempt counts toward MaxFailures
// and lengthens the next wait. A cancel that arrives during the result audit
// does not stop it, and the transition then runs unless the audit or store
// fails.
func (e *Engine) Reclaim(ctx context.Context, id string) (session.Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cur, err := e.dueSession(id)
	if err != nil {
		return cur, err
	}
	if err := ctx.Err(); err != nil {
		return cur, err
	}
	cur, err = e.store.CountReclaimAttempt(id)
	if err != nil {
		return session.Session{}, err
	}
	out := e.removeResources(ctx, cur.ID)
	if err := ctx.Err(); err != nil {
		return cur, err
	}
	return e.settle(cur, out)
}

func (e *Engine) dueSession(id string) (session.Session, error) {
	cur, err := e.load(id)
	if err != nil {
		return session.Session{}, err
	}
	if cur.State != session.StateReclaiming && cur.State != session.StateOrphaned {
		return session.Session{}, ErrNotReclaimable
	}
	// A wait longer than the cap means the clock stepped back; do not stall.
	if wait := Due(cur).Sub(e.now()); wait > 0 && wait <= backoffMax {
		return cur, ErrNotDue
	}
	return cur, nil
}

func (e *Engine) removeResources(ctx context.Context, id string) outcome {
	_ = e.provider.Reclaim(ctx, e.pool, id)
	listed, err := e.provider.List(ctx, e.pool)
	if err != nil {
		return outcome{result: resultFailed}
	}
	owned, _, err := provider.Classify(e.pool, listed)
	if err != nil {
		return outcome{result: resultFailed}
	}
	left := owned[id]
	if len(left) == 0 {
		return outcome{result: resultDeleted}
	}
	return outcome{result: resultPartial, remaining: kindsOf(left)}
}

func kindsOf(resources []provider.Resource) []string {
	var kinds []string
	for _, r := range resources {
		if kind, ok := auditKinds[r.Kind]; ok && !slices.Contains(kinds, kind) {
			kinds = append(kinds, kind)
		}
	}
	slices.Sort(kinds)
	return kinds
}

func (e *Engine) settle(cur session.Session, out outcome) (session.Session, error) {
	orphaned := isOrphaned(cur, out)
	data := reclaimData(cur, out, orphaned)
	if err := e.audit(envelope.AuditSessionReclaim, cur.ID, data); err != nil {
		return cur, err
	}
	return e.advance(cur, out, orphaned)
}

func isOrphaned(s session.Session, out outcome) bool {
	return s.State == session.StateOrphaned ||
		(out.result != resultDeleted && s.Attempts >= MaxFailures)
}

func (e *Engine) advance(
	cur session.Session, out outcome, orphaned bool,
) (session.Session, error) {
	switch {
	case out.result == resultDeleted:
		return e.store.Transition(cur.ID, cur.State, session.StateDeleted, cur.Reason)
	case orphaned && cur.State == session.StateReclaiming:
		next, err := e.store.Transition(cur.ID, cur.State, session.StateOrphaned, cur.Reason)
		if err != nil {
			return session.Session{}, err
		}
		return next, ErrReclaimIncomplete
	default:
		return cur, ErrReclaimIncomplete
	}
}

func reclaimData(s session.Session, out outcome, orphaned bool) map[string]any {
	data := map[string]any{
		attrkey.ReclaimResult:   out.result,
		attrkey.ReclaimAttempts: s.Attempts,
		attrkey.ReclaimOrphaned: orphaned,
	}
	if len(out.remaining) > 0 {
		data[attrkey.ReclaimRemaining] = out.remaining
	}
	return data
}
