package agent

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func control(state LifecycleState, timeout, action string) ControlRecord {
	return ControlRecord{
		Version:        LifecycleProtocolVersion,
		ClientID:       "client",
		State:          state,
		Timeout:        timeout,
		ShutdownAction: action,
	}
}

func TestMonitorStartsUnarmed(t *testing.T) {
	assert.False(t, (&LifecycleMonitor{}).Evaluate(t0.Add(24*time.Hour), t0, nil).Expired)
}

func TestMonitorSuspendedDuringSetupThenArmed(t *testing.T) {
	m := &LifecycleMonitor{}
	reset, err := m.Apply(control(LifecycleStateSetup, "1m", ""), t0)
	require.NoError(t, err)
	assert.False(t, reset)
	assert.False(t, m.Evaluate(t0.Add(time.Hour), t0, nil).Expired)
	ready := t0.Add(10 * time.Minute)
	_, err = m.Apply(control(LifecycleStateReady, "1m", ""), ready)
	require.NoError(t, err)
	assert.False(t, m.Evaluate(ready.Add(59*time.Second), t0, nil).Expired)
	assert.True(t, m.Evaluate(ready.Add(time.Minute), t0, nil).Expired)
}

func TestMonitorLegacyActivityKeepsWorkspaceAlive(t *testing.T) {
	m := &LifecycleMonitor{}
	_, err := m.Apply(control(LifecycleStateReady, "1m", ""), t0)
	require.NoError(t, err)
	recent := t0.Add(50 * time.Second)
	assert.False(t, m.Evaluate(t0.Add(100*time.Second), recent, nil).Expired)
	assert.True(t, m.Evaluate(t0.Add(111*time.Second), recent, nil).Expired)
}

func TestMonitorFailedSetupReleasesSuspensionWithFreshInterval(t *testing.T) {
	m := &LifecycleMonitor{}
	_, err := m.Apply(control(LifecycleStateSetup, "1m", ""), t0)
	require.NoError(t, err)
	failedAt := t0.Add(30 * time.Minute)
	_, err = m.Apply(control(LifecycleStateFailed, "1m", ""), failedAt)
	require.NoError(t, err)
	assert.False(t, m.Evaluate(failedAt.Add(30*time.Second), t0, nil).Expired)
	assert.True(t, m.Evaluate(failedAt.Add(time.Minute), t0, nil).Expired)
}

func TestMonitorReleaseIsIdempotentAndStartsIdleAtDisconnect(t *testing.T) {
	m := &LifecycleMonitor{}
	_, err := m.Apply(control(LifecycleStateSetup, "1m", ""), t0)
	require.NoError(t, err)
	m.Release("client", t0.Add(time.Hour))
	m.Release("client", t0.Add(time.Hour+30*time.Second))
	assert.False(t, m.Evaluate(t0.Add(time.Hour+59*time.Second), t0, nil).Expired)
	assert.True(t, m.Evaluate(t0.Add(time.Hour+time.Minute), t0, nil).Expired)
}

func TestMonitorDisabledPolicyDisarms(t *testing.T) {
	for _, rec := range []ControlRecord{
		control(LifecycleStateReady, "", ""), control(LifecycleStateReady, "1m", "none"),
	} {
		m := &LifecycleMonitor{}
		_, err := m.Apply(control(LifecycleStateReady, "1m", ""), t0)
		require.NoError(t, err)
		_, err = m.Apply(rec, t0)
		require.NoError(t, err)
		assert.False(t, m.Evaluate(t0.Add(24*time.Hour), t0, nil).Expired)
	}
}

func TestMonitorInvalidPolicyDoesNotReplaceCurrentDeadline(t *testing.T) {
	m := &LifecycleMonitor{}
	_, err := m.Apply(control(LifecycleStateReady, "1m", ""), t0)
	require.NoError(t, err)
	for _, rec := range []ControlRecord{control(LifecycleStateReady, "later", ""), control("bogus", "1h", "")} {
		_, err := m.Apply(rec, t0.Add(30*time.Second))
		require.Error(t, err)
	}
	assert.True(t, m.Evaluate(t0.Add(time.Minute), t0, nil).Expired)
}

func TestMonitorUnreadableLegacyActivityFailsSafe(t *testing.T) {
	m := &LifecycleMonitor{}
	m.ArmNow(time.Minute, t0)
	denied := &fs.PathError{Op: "stat", Path: "activity", Err: fs.ErrPermission}
	d := m.Evaluate(t0.Add(time.Hour), time.Time{}, denied)
	assert.False(t, d.Expired)
	assert.True(t, d.ActivityUnreadable)
}

func TestMonitorRejectsNewWorkOnceShutdownIsCommitted(t *testing.T) {
	m := &LifecycleMonitor{}
	m.ArmNow(time.Minute, t0)
	require.True(t, m.commitExpiry(t0.Add(time.Minute), t0, nil).Expired)
	_, err := m.Apply(control(LifecycleStateSetup, "1m", ""), t0.Add(time.Minute))
	assert.ErrorIs(t, err, ErrLifecycleOwnerMissing)
}

func TestMonitorRegistrationPreventsCommittingShutdown(t *testing.T) {
	m := &LifecycleMonitor{}
	m.ArmNow(time.Minute, t0)
	_, err := m.Apply(control(LifecycleStateActivity, "", ""), t0)
	require.NoError(t, err)
	assert.False(t, m.commitExpiry(t0.Add(time.Hour), t0, nil).Expired)
	m.Release("client", t0.Add(time.Hour))
	assert.True(t, m.commitExpiry(t0.Add(time.Hour+time.Minute), t0, nil).Expired)
}

func TestLifecycleLoopStopsIdleManagedDaemon(t *testing.T) {
	owner, dir := newTestOwner(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- (&LifecycleLoop{Owner: owner, Monitor: &LifecycleMonitor{}}).Run(ctx) }()
	client := connectNow(t, dir)
	require.NoError(t, client.Send(LifecycleStateReady, "300ms", ""))
	assert.ErrorIs(t, <-done, ErrInactivityExpired)
}

func TestActivityWriterTouchesImmediatelyPeriodicallyAndOnExit(t *testing.T) {
	ticks := make(chan time.Time)
	var mu sync.Mutex
	var touched []time.Time
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(touched) }
	w := ActivityWriter{
		Path: "activity", Interval: time.Second,
		Touch: func(_ string, now time.Time) error {
			mu.Lock()
			defer mu.Unlock()
			touched = append(touched, now)
			return nil
		},
		Now:  func() time.Time { return t0 },
		Tick: func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	require.Eventually(t, func() bool { return count() == 1 }, time.Second, time.Millisecond)
	ticks <- time.Time{}
	require.Eventually(t, func() bool { return count() == 2 }, time.Second, time.Millisecond)
	cancel()
	<-done
	assert.Equal(t, 3, count())
}

func TestTouchByWriteAdvancesModTimeForNonOwners(t *testing.T) {
	path := filepath.Join(t.TempDir(), "activity")
	require.NoError(t, TouchActivity(path, t0))
	require.NoError(t, os.Chtimes(path, t0, t0))
	require.NoError(t, touchByWrite(path))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), info.ModTime(), 2*time.Second)
}
