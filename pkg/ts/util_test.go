package ts

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

// waitUntil polls cond until it reports true, failing the test if it does
// not become true within timeout.
func waitUntil(t *testing.T, timeout time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(time.Millisecond)
	}
}

// fakeIPNWatcher replays a fixed sequence of notifications, then blocks until
// the test signals it to report the watch as closed.
type fakeIPNWatcher struct {
	notifs []ipn.Notify
	idx    atomic.Int32
	closed chan struct{}
}

func (w *fakeIPNWatcher) Next() (ipn.Notify, error) {
	i := w.idx.Load()
	if int(i) < len(w.notifs) {
		w.idx.Add(1)
		return w.notifs[i], nil
	}
	<-w.closed
	return ipn.Notify{}, errors.New("watcher closed")
}

// TestWatchNetmapDrainsBurstWhileStatusFetchBlocked verifies that a burst of
// notifications arriving while fetchStatus is in flight does not stall
// notification draining: watchNetmap must keep calling watcher.Next() so a
// burst larger than the IPN bus's 128-entry queue never causes tailscaled to
// close the watch, and must coalesce the burst into a single follow-up fetch.
func TestWatchNetmapDrainsBurstWhileStatusFetchBlocked(t *testing.T) {
	const burst = 129

	notifs := make([]ipn.Notify, burst)
	for i := range notifs {
		notifs[i] = ipn.Notify{SelfChange: &tailcfg.Node{}}
	}
	watcher := &fakeIPNWatcher{notifs: notifs, closed: make(chan struct{})}

	firstFetchStarted := make(chan struct{})
	unblockFirstFetch := make(chan struct{})
	var fetchCount atomic.Int32

	fetchStatus := func(context.Context) (*ipnstate.Status, error) { //nolint:unparam // exercises the success path only
		n := fetchCount.Add(1)
		if n == 1 {
			close(firstFetchStarted)
			<-unblockFirstFetch
		}
		return &ipnstate.Status{}, nil
	}

	var callbackCount atomic.Int32
	callback := func(*ipnstate.Status) { callbackCount.Add(1) }

	errc := make(chan error, 1)
	go func() {
		errc <- watchNetmap(context.Background(), watcher, fetchStatus, callback)
	}()

	select {
	case <-firstFetchStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first status fetch never started")
	}

	waitUntil(t, 5*time.Second, "watcher stalled while status fetch was blocked", func() bool {
		return watcher.idx.Load() == burst
	})

	close(unblockFirstFetch)

	waitUntil(t, 5*time.Second, "expected a coalesced follow-up fetch", func() bool {
		return fetchCount.Load() >= 2
	})

	if got := fetchCount.Load(); got != 2 {
		t.Errorf(
			"fetchStatus called %d times, want exactly 2 (initial + one coalesced follow-up)",
			got,
		)
	}
	if got := callbackCount.Load(); got != 2 {
		t.Errorf("callback invoked %d times, want 2", got)
	}

	close(watcher.closed)
	if err := <-errc; err == nil {
		t.Fatal("watchNetmap returned nil error after watcher closed, want an error")
	}
}
