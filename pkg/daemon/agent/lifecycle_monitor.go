package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

const (
	idleCheckInterval = 500 * time.Millisecond
	maxCheckInterval  = time.Second
	// unreadableLogAfter is how many consecutive failed checks precede a warning.
	unreadableLogAfter = 3
)

// Decision is the outcome of one inactivity check.
type Decision struct {
	Expired bool
	// ActivityUnreadable means the activity file could not be read, so the check
	// failed safe and the timer is not effective.
	ActivityUnreadable bool
	Failures           int
	// RecordingFailed means a writer cannot record activity, so an old timestamp
	// proves nothing and expiry is withheld until recording recovers.
	RecordingFailed bool
}

// LifecycleMonitor decides when the container has been idle for too long. It
// holds no clock or file handles; callers pass observations in.
type LifecycleMonitor struct {
	mu        sync.Mutex
	timeout   time.Duration
	armed     bool
	suspended bool
	armedAt   time.Time
	failures  int
	// recordingFailed is set while any activity writer reports it cannot record.
	recordingFailed bool
}

// Apply updates the timer from a control record. It reports whether the activity
// timestamp should be reset to now.
func (m *LifecycleMonitor) Apply(rec ControlRecord, now time.Time) (bool, error) {
	var timeout time.Duration
	if rec.Timeout != "" {
		var err error
		if timeout, err = time.ParseDuration(rec.Timeout); err != nil || timeout < 0 {
			return false, fmt.Errorf("invalid lifecycle timeout %q", rec.Timeout)
		}
	}
	enabled := timeout > 0 && rec.ShutdownAction != config.ShutdownActionNone

	m.mu.Lock()
	defer m.mu.Unlock()
	m.timeout = timeout
	switch rec.State {
	case LifecycleStateSetup:
		m.suspended = true
		return false, nil
	case LifecycleStateReady, LifecycleStateFailed:
		m.suspended = false
		m.armed = enabled
		m.armedAt = now
		m.failures = 0
		return enabled, nil
	default:
		return false, fmt.Errorf("unknown lifecycle state %q", rec.State)
	}
}

// ArmNow arms the timer without a handshake, for daemons that own no setup.
func (m *LifecycleMonitor) ArmNow(timeout time.Duration, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.timeout, m.armed, m.suspended, m.armedAt = timeout, timeout > 0, false, now
}

// SetRecordingFailed records whether any activity writer currently fails to
// record activity.
func (m *LifecycleMonitor) SetRecordingFailed(failed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recordingFailed = failed
}

// Evaluate checks the idle time. A missing activity file counts as no activity
// since arming; any other read error fails safe.
func (m *LifecycleMonitor) Evaluate(now, modTime time.Time, statErr error) Decision {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.armed || m.suspended {
		return Decision{}
	}
	if m.recordingFailed {
		return Decision{RecordingFailed: true}
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

// CheckInterval bounds how long the loop sleeps between checks.
func (m *LifecycleMonitor) CheckInterval() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.armed {
		return idleCheckInterval
	}
	return min(maxCheckInterval, max(m.timeout/4, minTickInterval))
}

// LifecycleLoop is the production adapter around LifecycleMonitor.
type LifecycleLoop struct {
	// Owner is nil for daemons armed directly with ArmNow.
	Owner        *LifecycleOwner
	Monitor      *LifecycleMonitor
	ActivityPath string
}

// Run returns ErrInactivityExpired when the container went idle, nil when ctx
// ended first.
func (l *LifecycleLoop) Run(ctx context.Context) error {
	var controlErr, unreadable, recordingFailed bool
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
		now := time.Now()
		controlErr = l.applyControl(now, controlErr)
		l.Monitor.SetRecordingFailed(ActivityRecordingFailing(l.ActivityPath))

		fi, err := os.Stat(l.ActivityPath)
		var mod time.Time
		if err == nil {
			mod = fi.ModTime()
		}
		d := l.Monitor.Evaluate(now, mod, err)
		unreadable = logUnreadable(d, err, unreadable)
		recordingFailed = logRecordingFailed(d, recordingFailed)
		if d.Expired {
			log.Infof("container idle for the configured inactivity timeout, shutting down")
			return ErrInactivityExpired
		}
		timer.Reset(l.Monitor.CheckInterval())
	}
}

func (l *LifecycleLoop) applyControl(now time.Time, hadErr bool) bool {
	if l.Owner == nil {
		return false
	}
	rec, changed, err := l.Owner.PollControl()
	if err != nil {
		if !hadErr {
			log.Errorf("read container lifecycle control record: %v", err)
		}
		return true
	}
	if !changed {
		return false
	}
	return l.applyRecord(rec, now)
}

func (l *LifecycleLoop) applyRecord(rec ControlRecord, now time.Time) bool {
	reset, applyErr := l.Monitor.Apply(rec, now)
	if applyErr == nil && reset {
		if err := TouchActivity(l.ActivityPath, now); err != nil {
			log.Errorf("reset activity timestamp: %v", err)
		}
	}
	// Acknowledge only after the state change took effect: setup waits for the
	// ack before starting work the timer must not interrupt.
	ackErr := l.Owner.Acknowledge(rec, applyErr)
	if applyErr != nil {
		log.Errorf("apply container lifecycle control record: %v", applyErr)
	}
	if ackErr != nil {
		log.Errorf("acknowledge container lifecycle control record: %v", ackErr)
	}
	return applyErr != nil || ackErr != nil
}

func logRecordingFailed(d Decision, was bool) bool {
	switch {
	case d.RecordingFailed && !was:
		log.Errorf("an activity writer cannot record activity; " +
			"inactivity shutdown is withheld until recording recovers")
		return true
	case !d.RecordingFailed && was:
		log.Infof("activity recording recovered; inactivity timeout is effective")
		return false
	}
	return was
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

// ActivityIntervalForLifecycle derives the activity cadence from the published
// policy, falling back to the default.
func ActivityIntervalForLifecycle() time.Duration {
	timeout, ok := ReadLifecycleTimeout(pkgconfig.ContainerLifecycleDir)
	if !ok {
		return DefaultActivityInterval
	}
	return HeartbeatInterval(timeout)
}
