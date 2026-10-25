package agent

import (
	"context"
	"errors"
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
		Version: LifecycleProtocolVersion, State: state, Timeout: timeout, ShutdownAction: action,
	}
}

func TestMonitorStartsUnarmed(t *testing.T) {
	m := &LifecycleMonitor{}

	d := m.Evaluate(t0.Add(24*time.Hour), t0, nil)

	assert.False(t, d.Expired)
}

func TestMonitorSuspendedDuringSetupThenArmed(t *testing.T) {
	m := &LifecycleMonitor{}

	reset, err := m.Apply(control(LifecycleStateSetup, "1m", ""), t0)
	require.NoError(t, err)
	assert.False(t, reset)
	assert.False(t, m.Evaluate(t0.Add(time.Hour), t0, nil).Expired, "suspended by setup")

	ready := t0.Add(10 * time.Minute)
	reset, err = m.Apply(control(LifecycleStateReady, "1m", ""), ready)
	require.NoError(t, err)
	assert.True(t, reset, "arming resets the activity timestamp")

	assert.False(t, m.Evaluate(ready.Add(59*time.Second), t0, nil).Expired,
		"old activity cannot expire a freshly armed timer")
	assert.True(t, m.Evaluate(ready.Add(time.Minute), t0, nil).Expired)
}

func TestMonitorActivityKeepsWorkspaceAlive(t *testing.T) {
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

func TestMonitorRepeatedSetupSuspendsAgainAndUpdatesPolicy(t *testing.T) {
	m := &LifecycleMonitor{}
	_, err := m.Apply(control(LifecycleStateReady, "1m", ""), t0)
	require.NoError(t, err)

	_, err = m.Apply(control(LifecycleStateSetup, "5m", ""), t0.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, m.Evaluate(t0.Add(2*time.Hour), t0, nil).Expired)

	rearmed := t0.Add(3 * time.Hour)
	_, err = m.Apply(control(LifecycleStateReady, "5m", ""), rearmed)
	require.NoError(t, err)
	assert.False(t, m.Evaluate(rearmed.Add(4*time.Minute), rearmed, nil).Expired)
	assert.True(t, m.Evaluate(rearmed.Add(5*time.Minute), rearmed, nil).Expired)
}

func TestMonitorDisabledPolicyDisarms(t *testing.T) {
	for name, rec := range map[string]ControlRecord{
		"empty timeout": control(LifecycleStateReady, "", ""),
		"shutdown none": control(LifecycleStateReady, "1m", "none"),
	} {
		t.Run(name, func(t *testing.T) {
			m := &LifecycleMonitor{}
			_, err := m.Apply(control(LifecycleStateReady, "1m", ""), t0)
			require.NoError(t, err)

			reset, err := m.Apply(rec, t0)

			require.NoError(t, err)
			assert.False(t, reset)
			assert.False(t, m.Evaluate(t0.Add(24*time.Hour), t0, nil).Expired)
		})
	}
}

func TestMonitorRejectsInvalidControlRecords(t *testing.T) {
	m := &LifecycleMonitor{}

	_, err := m.Apply(control(LifecycleStateReady, "later", ""), t0)
	assert.Error(t, err)
	_, err = m.Apply(control("bogus", "1m", ""), t0)
	assert.Error(t, err)
}

func TestMonitorUnreadableActivityFailsSafe(t *testing.T) {
	m := &LifecycleMonitor{}
	_, err := m.Apply(control(LifecycleStateReady, "1m", ""), t0)
	require.NoError(t, err)
	denied := &fs.PathError{Op: "stat", Path: "activity", Err: fs.ErrPermission}

	var d Decision
	for range 5 {
		d = m.Evaluate(t0.Add(time.Hour), time.Time{}, denied)
	}

	assert.False(t, d.Expired, "an active workspace must not stop because activity is unreadable")
	assert.True(t, d.ActivityUnreadable)
	assert.Equal(t, 5, d.Failures)
}

func TestMonitorMissingActivityFileCountsFromArming(t *testing.T) {
	m := &LifecycleMonitor{}
	_, err := m.Apply(control(LifecycleStateReady, "1m", ""), t0)
	require.NoError(t, err)

	d := m.Evaluate(t0.Add(time.Minute), time.Time{}, os.ErrNotExist)

	assert.True(t, d.Expired)
	assert.False(t, d.ActivityUnreadable)
}

func TestIntervalsScaleWithShortTimeouts(t *testing.T) {
	assert.Equal(t, 10*time.Second, HeartbeatInterval(0))
	assert.Equal(t, 10*time.Second, HeartbeatInterval(time.Minute), "minute scale keeps 10s")
	assert.Equal(t, 10*time.Second, HeartbeatInterval(30*time.Second))
	assert.Equal(t, 2*time.Second, HeartbeatInterval(6*time.Second))
	assert.LessOrEqual(t, HeartbeatInterval(900*time.Millisecond), 300*time.Millisecond)

	m := &LifecycleMonitor{}
	assert.Equal(t, idleCheckInterval, m.CheckInterval(), "unarmed polls for control records")
	_, err := m.Apply(control(LifecycleStateReady, "1m", ""), t0)
	require.NoError(t, err)
	assert.Equal(t, time.Second, m.CheckInterval())
	_, err = m.Apply(control(LifecycleStateReady, "2s", ""), t0)
	require.NoError(t, err)
	assert.Equal(t, 500*time.Millisecond, m.CheckInterval())
}

func TestLifecycleLoopStopsIdleManagedDaemon(t *testing.T) {
	owner, dir := newTestOwner(t)
	activity := filepath.Join(t.TempDir(), "activity")
	loop := &LifecycleLoop{Owner: owner, Monitor: &LifecycleMonitor{}, ActivityPath: activity}
	require.NoError(t, connectNow(t, dir).Send(LifecycleStateReady, "300ms", ""))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := loop.Run(ctx)

	assert.True(t, errors.Is(err, ErrInactivityExpired), "got %v", err)
}

func TestLifecycleLoopStaysUnarmedWithoutHandshake(t *testing.T) {
	owner, _ := newTestOwner(t)
	loop := &LifecycleLoop{
		Owner:        owner,
		Monitor:      &LifecycleMonitor{},
		ActivityPath: filepath.Join(t.TempDir(), "activity"),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	err := loop.Run(ctx)

	assert.NoError(t, err)
}

func TestLifecycleLoopRespectsFreshActivity(t *testing.T) {
	owner, dir := newTestOwner(t)
	activity := filepath.Join(t.TempDir(), "activity")
	loop := &LifecycleLoop{Owner: owner, Monitor: &LifecycleMonitor{}, ActivityPath: activity}
	require.NoError(t, connectNow(t, dir).Send(LifecycleStateReady, "400ms", ""))

	ctx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	go ActivityWriter{Path: activity, Interval: 100 * time.Millisecond}.Run(ctx)

	assert.NoError(t, loop.Run(ctx), "continuous activity must keep the workspace running")
}

func TestActivityWriterTouchesImmediatelyPeriodicallyAndOnExit(t *testing.T) {
	ticks := make(chan time.Time)
	var mu sync.Mutex
	var touched []time.Time
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(touched) }
	w := ActivityWriter{
		Path:     "activity",
		Interval: time.Second,
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

	require.Eventually(t, func() bool { return count() == 1 }, time.Second, time.Millisecond)
	ticks <- time.Time{}
	require.Eventually(t, func() bool { return count() == 2 }, time.Second, time.Millisecond)
	cancel()
	<-done

	assert.Equal(t, 3, count(), "start, one tick, and a final touch at exit")
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
