// Package engine moves pool sessions through their states. Each state change
// writes its audit event first, so a failed audit write leaves the session
// where it was. The engine is the only writer of session state and serializes
// its own calls. Neither a fake nor a real provider is started here.
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
	Transition(id string, from, to session.State, reason string) (session.Session, error)
	CountReclaimAttempt(id string) (session.Session, error)
}

// Config wires an Engine. Now defaults to time.Now.
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
