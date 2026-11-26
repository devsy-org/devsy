// Package engine moves pool sessions through their states. Each state change
// writes its audit event first, so a failed audit write leaves the session
// where it was. The engine is the only writer of session state and serializes
// its own calls. Neither a fake nor a real provider is started here.
//
// Run one Engine per pool. The mutex only orders calls on one Engine, so two
// Engines on one store both write the audit event for the same change. The
// pool lock keeps a second process out of the store.
//
// The store holds the committed state and reason. An audit event is written
// before its transition, so it records intent: a failed or interrupted
// transition can leave an event for a change that did not commit, or a change
// that did (a directory sync error after the rename), so reload to find out.
// A retry can log the same intended change again, as session.end possibly
// with another reason, or as session.reclaim for the next attempt. Read state
// and reason from the store, and treat repeated events for one session as
// retries.
package engine

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/devsy-org/devsy-signals/envelope"
	"github.com/devsy-org/devsy/pkg/pool/audit"
	"github.com/devsy-org/devsy/pkg/pool/poolconfig"
	"github.com/devsy-org/devsy/pkg/pool/provider"
	"github.com/devsy-org/devsy/pkg/pool/session"
)

const actorID = "pool"

// Errors a caller can test with errors.Is.
var (
	// ErrAuditFailed wraps any audit write failure. The session is unchanged.
	ErrAuditFailed = errors.New("audit write failed")
	// ErrInvalidEndReason reports an end reason outside the audit enum.
	ErrInvalidEndReason = errors.New("end reason is not valid")
	// ErrForeignPool reports a session record that belongs to another pool.
	ErrForeignPool = errors.New("session belongs to another pool")
	// ErrNotEndable reports a session that cannot end from its state.
	ErrNotEndable = errors.New("session cannot end from its state")
	// ErrNotReclaimable reports a session that is not reclaiming or orphaned.
	ErrNotReclaimable = errors.New("session is not being reclaimed")
	// ErrNotDue reports a reclaim attempt that came before its backoff ended.
	ErrNotDue = errors.New("reclaim attempt is not due")
	// ErrReclaimIncomplete reports an attempt that left resources behind. The
	// attempt is recorded and the session will be retried.
	ErrReclaimIncomplete = errors.New("reclaim did not remove every resource")
)

// Auditor appends audit events. *audit.Writer satisfies it.
type Auditor interface {
	Write(audit.Event) error
}

// Store is the part of *session.Store the engine uses.
type Store interface {
	Load(id string) (session.Session, error)
	List() ([]session.Session, error)
	Transition(id string, from, to session.State, reason string) (session.Session, error)
	CountReclaimAttempt(id string) (session.Session, error)
}

// Config wires an Engine. Now defaults to time.Now. The engine holds its mutex
// across store, provider and audit calls, so none of them may call back into
// the same Engine, and a provider must return promptly once its context is
// canceled or every other call on the Engine waits behind it.
type Config struct {
	Pool     string
	Store    Store
	Audit    Auditor
	Provider provider.Provider
	Now      func() time.Time
}

// Engine applies state changes to the sessions of one pool.
type Engine struct {
	mu       sync.Mutex
	pool     string
	store    Store
	auditor  Auditor
	provider provider.Provider
	now      func() time.Time
}

// New checks cfg and returns an Engine.
func New(cfg Config) (*Engine, error) {
	if !poolconfig.ValidPoolName(cfg.Pool) {
		return nil, provider.ErrInvalidPool
	}
	if cfg.Store == nil || cfg.Audit == nil || cfg.Provider == nil {
		return nil, errors.New("engine requires a store, an auditor and a provider")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Engine{
		pool: cfg.Pool, store: cfg.Store, auditor: cfg.Audit,
		provider: cfg.Provider, now: cfg.Now,
	}, nil
}

// load reads a session and refuses one the store holds for another pool, so a
// miswired store cannot make the engine audit or delete the wrong pool's work.
func (e *Engine) load(id string) (session.Session, error) {
	cur, err := e.store.Load(id)
	if err != nil {
		return session.Session{}, err
	}
	if cur.Pool != e.pool {
		return session.Session{}, ErrForeignPool
	}
	return cur, nil
}

func (e *Engine) audit(typ, id string, data map[string]any) error {
	err := e.auditor.Write(audit.Event{
		Type:    typ,
		Session: id,
		Actor:   envelope.Actor{Kind: envelope.ActorSystem, ID: actorID},
		Data:    data,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrAuditFailed, err)
	}
	return nil
}
