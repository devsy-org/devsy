package engine

import (
	"github.com/devsy-org/devsy-signals/attrkey"
	"github.com/devsy-org/devsy-signals/envelope"
	"github.com/devsy-org/devsy/pkg/pool/session"
)

// EndReason is a devsy.session.end_reason value. It is also stored as the
// session reason.
type EndReason string

// End reasons from the audit allow-list.
const (
	EndCompleted   EndReason = "completed"
	EndFailed      EndReason = "failed"
	EndIdle        EndReason = "idle"
	EndTTL         EndReason = "ttl"
	EndJobTimeout  EndReason = "job_timeout"
	EndInterrupted EndReason = "interrupted"
	EndPoolRestart EndReason = "pool_restart"
)

func (r EndReason) valid() bool {
	switch r {
	case EndCompleted, EndFailed, EndIdle, EndTTL, EndJobTimeout, EndInterrupted, EndPoolRestart:
		return true
	default:
		return false
	}
}

// Fail moves a spawning, ready, busy or idle session to failed and records
// session.end first.
func (e *Engine) Fail(id string, reason EndReason) (session.Session, error) {
	return e.end(id, session.StateFailed, reason)
}

// BeginReclaim moves a queued, spawning, ready, busy, idle or failed session
// to reclaiming. A live session records session.end first. A failed session
// already did, so it keeps its reason, ignores reason and records nothing.
func (e *Engine) BeginReclaim(id string, reason EndReason) (session.Session, error) {
	return e.end(id, session.StateReclaiming, reason)
}

func (e *Engine) end(id string, to session.State, reason EndReason) (session.Session, error) {
	if !reason.valid() {
		return session.Session{}, ErrInvalidEndReason
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	cur, err := e.store.Load(id)
	if err != nil {
		return session.Session{}, err
	}
	if !endable(cur.State) || !session.CanTransition(cur.State, to) {
		return session.Session{}, ErrNotEndable
	}
	if cur.State == session.StateFailed {
		reason = EndReason(cur.Reason)
	} else if err := e.writeEnd(cur, reason); err != nil {
		return session.Session{}, err
	}
	return e.store.Transition(id, cur.State, to, string(reason))
}

// endable excludes orphaned, which can also reach reclaiming but already
// ended, and the terminal states.
func endable(s session.State) bool {
	switch s {
	case session.StateQueued, session.StateSpawning, session.StateReady,
		session.StateBusy, session.StateIdle, session.StateFailed:
		return true
	default:
		return false
	}
}

func (e *Engine) writeEnd(s session.Session, reason EndReason) error {
	elapsed := max(e.now().Sub(s.Created).Milliseconds(), 0)
	return e.audit(envelope.AuditSessionEnd, s.ID, map[string]any{
		attrkey.SessionEndReason: string(reason),
		attrkey.DurationMS:       elapsed,
	})
}
