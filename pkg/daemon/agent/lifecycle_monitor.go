package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

const (
	idleCheckInterval  = 500 * time.Millisecond
	maxCheckInterval   = time.Second
	unreadableLogAfter = 3
)

// Decision is the outcome of one inactivity check.
type Decision struct {
	Expired            bool
	ActivityUnreadable bool
	Failures           int
}

// LifecycleMonitor serializes protection changes with the decision to stop.
type LifecycleMonitor struct {
	mu        sync.Mutex
	timeout   time.Duration
	armed     bool
	armedAt   time.Time
	failures  int
	protected map[string]struct{}
	stopping  bool
}

// Apply changes one client's protection. Acknowledgements are sent only after
// this method returns; no filesystem write is needed to retain protection.
func (m *LifecycleMonitor) Apply(rec ControlRecord, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopping {
		return false, ErrLifecycleOwnerMissing
	}
	if rec.State == LifecycleStateActivity {
		m.protect(rec.ClientID)
		return false, nil
	}
	if rec.State == LifecycleStateRelease {
		m.release(rec.ClientID, now)
		return false, nil
	}
	timeout, err := parseLifecyclePolicy(rec)
	if err != nil {
		return false, err
	}
	m.timeout = timeout
	m.armed = timeout > 0 && rec.ShutdownAction != config.ShutdownActionNone
	if rec.State == LifecycleStateSetup {
		m.protect(rec.ClientID)
		return false, nil
	}
	delete(m.protected, rec.ClientID)
	m.armedAt = now
	m.failures = 0
	return m.armed, nil
}

func parseLifecyclePolicy(rec ControlRecord) (time.Duration, error) {
	switch rec.State {
	case LifecycleStateSetup, LifecycleStateReady, LifecycleStateFailed:
	default:
		return 0, fmt.Errorf("unknown lifecycle state %q", rec.State)
	}
	if rec.Timeout == "" {
		return 0, nil
	}
	timeout, err := time.ParseDuration(rec.Timeout)
	if err != nil || timeout < 0 {
		return 0, fmt.Errorf("invalid lifecycle timeout %q", rec.Timeout)
	}
	return timeout, nil
}

// Release ends only this client's protection and starts a fresh idle interval.
func (m *LifecycleMonitor) Release(id string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.release(id, now)
}

// ArmNow arms daemons that have no managed setup handshake.
func (m *LifecycleMonitor) ArmNow(timeout time.Duration, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timeout, m.armed, m.armedAt = timeout, timeout > 0, now
}

// Evaluate observes the timer without committing the daemon to shutdown.
func (m *LifecycleMonitor) Evaluate(now, modTime time.Time, statErr error) Decision {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.evaluate(now, modTime, statErr)
}

// CheckInterval bounds the delay before observing idle time or process death.
func (m *LifecycleMonitor) CheckInterval() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.armed {
		return idleCheckInterval
	}
	return min(maxCheckInterval, max(m.timeout/4, minTickInterval))
}

func (m *LifecycleMonitor) protect(id string) {
	if m.protected == nil {
		m.protected = make(map[string]struct{})
	}
	m.protected[id] = struct{}{}
}

func (m *LifecycleMonitor) release(id string, now time.Time) {
	if _, ok := m.protected[id]; ok {
		delete(m.protected, id)
		m.armedAt = now
	}
}

func (m *LifecycleMonitor) evaluate(now, modTime time.Time, statErr error) Decision {
	if !m.armed || len(m.protected) > 0 {
		return Decision{}
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		m.failures++
		return Decision{ActivityUnreadable: true, Failures: m.failures}
	}
	m.failures = 0
	last := m.armedAt
	if statErr == nil && modTime.After(last) {
		last = modTime
	}
	return Decision{Expired: now.Sub(last) >= m.timeout}
}

func (m *LifecycleMonitor) commitExpiry(now, modTime time.Time, statErr error) Decision {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.evaluate(now, modTime, statErr)
	// A racing registration must be rejected once shutdown has won this lock.
	m.stopping = m.stopping || d.Expired
	return d
}

// LifecycleLoop uses live registrations for managed containers and the legacy
// activity file only for daemons without a managed owner.
type LifecycleLoop struct {
	Owner        *LifecycleOwner
	Monitor      *LifecycleMonitor
	ActivityPath string
}

func lifecycleContextError(ctx context.Context) error {
	cause := context.Cause(ctx)
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return nil
	}
	return cause
}

func (l *LifecycleLoop) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stopOwner := l.startOwner(ctx, cancel)
	defer stopOwner()
	var unreadable bool
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return lifecycleContextError(ctx)
		case <-timer.C:
		}
		d, err := l.observe(time.Now())
		unreadable = logUnreadable(d, err, unreadable)
		if d.Expired {
			log.Infof("container idle for the configured inactivity timeout, shutting down")
			return ErrInactivityExpired
		}
		timer.Reset(l.Monitor.CheckInterval())
	}
}

func (l *LifecycleLoop) startOwner(ctx context.Context, cancel context.CancelCauseFunc) func() {
	if l.Owner == nil {
		return func() {}
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		err := l.Owner.Serve(ctx, l.Monitor)
		if ctx.Err() == nil {
			if err == nil {
				err = ErrLifecycleOwnerMissing
			}
			cancel(err)
		}
	}()
	return func() { cancel(nil); _ = l.Owner.Close(); <-stopped }
}

func (l *LifecycleLoop) observe(now time.Time) (Decision, error) {
	if l.Owner != nil {
		l.Owner.pruneDeadClients(l.Monitor, now)
		return l.Monitor.commitExpiry(now, time.Time{}, nil), nil
	}
	fi, err := os.Stat(l.ActivityPath)
	var mod time.Time
	if err == nil {
		mod = fi.ModTime()
	}
	return l.Monitor.commitExpiry(now, mod, err), err
}

func logUnreadable(d Decision, err error, was bool) bool {
	switch {
	case d.ActivityUnreadable && d.Failures >= unreadableLogAfter && !was:
		log.Errorf(
			"cannot read activity file; inactivity timeout is not effective until it is readable: %v",
			err,
		)
		return true
	case !d.ActivityUnreadable && was:
		log.Infof("activity file readable again; inactivity timeout is effective")
		return false
	}
	return was
}
