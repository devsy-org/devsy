package ssh

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newRecordingCounter(t *testing.T, timeout time.Duration) (*connectionCounter, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	c := newConnectionCounter(context.Background(), timeout, func() {
		calls.Add(1)
	}, "test")
	return c, &calls
}

func TestConnectionCounter_AddDecTracksCountWithoutTimeout(t *testing.T) {
	c, calls := newRecordingCounter(t, 0)

	c.Add()
	c.Add()
	c.Dec()
	c.Dec()

	assert.Equal(t, 0, c.connections, "count should return to zero after balanced Add/Dec")
	assert.Equal(t, int32(0), calls.Load(),
		"onTimeout must not fire when timeout is zero")
}

func TestConnectionCounter_IdleTimeoutFires(t *testing.T) {
	c, calls := newRecordingCounter(t, 10*time.Millisecond)

	c.Add()
	c.Dec()

	require.Eventually(t, func() bool { return calls.Load() == 1 },
		time.Second, time.Millisecond, "onTimeout should fire once after going idle")
	assert.Equal(t, 0, c.connections, "count must remain zero when the timeout fires")
}

func TestConnectionCounter_InitialZeroConnectionsStartsTimeout(t *testing.T) {
	c, calls := newRecordingCounter(t, 10*time.Millisecond)
	defer c.Close()

	require.Eventually(t, func() bool { return calls.Load() == 1 },
		time.Second, time.Millisecond, "an initially idle counter should time out")
}

func TestConnectionCounter_TimeoutDispatchRejectsNewConnection(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	c := newConnectionCounter(context.Background(), 10*time.Millisecond, func() {
		close(started)
		<-release
	}, "test")

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for timeout dispatch")
	}
	assert.False(t, c.Add(), "connections must be rejected after timeout dispatch starts")
	assert.Zero(t, c.connections)
	close(release)
	c.Close()
}

func TestConnectionCounter_CloseStopsPendingTimeout(t *testing.T) {
	c, calls := newRecordingCounter(t, 10*time.Millisecond)
	c.Close()

	require.Never(t, func() bool { return calls.Load() > 0 },
		50*time.Millisecond, time.Millisecond)
}

func TestConnectionCounter_NewConnectionBeforeTimeoutCancelsIt(t *testing.T) {
	c, calls := newRecordingCounter(t, 50*time.Millisecond)

	c.Add()
	c.Dec() // starts the idle timer

	// Arrive a new connection before the idle timer elapses.
	time.Sleep(10 * time.Millisecond)
	c.Add()

	require.Never(t, func() bool { return calls.Load() > 0 },
		80*time.Millisecond, 10*time.Millisecond,
		"onTimeout must not fire while a connection is active")

	c.Dec() // now let it idle for real

	require.Eventually(t, func() bool { return calls.Load() == 1 },
		time.Second, time.Millisecond, "onTimeout should fire after the final idle")
}

func TestConnectionCounter_SpuriousDecClampsAtZero(t *testing.T) {
	c, calls := newRecordingCounter(t, 10*time.Millisecond)

	c.Add()
	c.Dec()
	c.Dec() // spurious Dec with no matching Add

	// A subsequent Add must be tracked against a zero baseline, not a negative one.
	assert.True(t, c.Add())
	assert.Equal(t, 1, c.connections, "Add after a spurious Dec must account correctly")
	c.Dec()

	require.Eventually(t, func() bool { return calls.Load() == 1 },
		time.Second, time.Millisecond, "onTimeout should fire exactly once")
	assert.Equal(t, 0, c.connections, "count must never go negative after a spurious Dec")
}

func TestConnectionCounter_CancelledContextDoesNotFireTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	c := newConnectionCounter(ctx, 10*time.Millisecond, func() {
		calls.Add(1)
	}, "test")

	c.Add()
	c.Dec() // schedules a timer guarded by ctx

	cancel() // context done before the timer fires

	require.Never(t, func() bool { return calls.Load() > 0 },
		100*time.Millisecond, 10*time.Millisecond,
		"onTimeout must not fire once the context is cancelled")
}
