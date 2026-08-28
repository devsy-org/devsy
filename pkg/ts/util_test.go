package ts

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"testing"
	"time"

	"tailscale.com/ipn"
	"tailscale.com/ipn/ipnstate"
	"tailscale.com/tailcfg"
)

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

func waitFor(t *testing.T, timeout time.Duration, msg string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(timeout):
		t.Fatal(msg)
	}
}

func selfChangeNotifs(n int) []ipn.Notify {
	notifs := make([]ipn.Notify, n)
	for i := range notifs {
		notifs[i] = ipn.Notify{SelfChange: &tailcfg.Node{}}
	}
	return notifs
}

// fakeIPNWatcher blocks Next's second call on afterFirst so a test can force
// a burst to arrive while a status fetch is still in flight.
type fakeIPNWatcher struct {
	notifs     []ipn.Notify
	idx        atomic.Int32
	afterFirst chan struct{}
	closed     chan struct{}
}

func (w *fakeIPNWatcher) Next() (ipn.Notify, error) {
	i := w.idx.Load()
	if i == 1 {
		<-w.afterFirst
	}
	if int(i) < len(w.notifs) {
		w.idx.Add(1)
		return w.notifs[i], nil
	}
	<-w.closed
	return ipn.Notify{}, errors.New("watcher closed")
}

// Regression test for the burst-drain/coalescing bug: draining must not
// stall behind a blocked status fetch.
func TestWatchNetmapDrainsBurstWhileStatusFetchBlocked(t *testing.T) {
	const burst = 129

	watcher := &fakeIPNWatcher{
		notifs:     selfChangeNotifs(burst),
		afterFirst: make(chan struct{}),
		closed:     make(chan struct{}),
	}

	firstFetchStarted := make(chan struct{})
	unblockFirstFetch := make(chan struct{})
	var fetchCount atomic.Int32

	fetchStatus := func(context.Context) (*ipnstate.Status, error) { //nolint:unparam // exercises the success path only
		if fetchCount.Add(1) == 1 {
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

	waitFor(t, 5*time.Second, "first status fetch never started", firstFetchStarted)
	close(watcher.afterFirst)

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

func TestToUint16Port(t *testing.T) {
	cases := []struct {
		name    string
		port    int
		want    uint16
		wantErr bool
	}{
		{name: "negative", port: -1, wantErr: true},
		{name: "zero", port: 0, want: 0},
		{name: "max", port: math.MaxUint16, want: math.MaxUint16},
		{name: "over max", port: math.MaxUint16 + 1, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ToUint16Port(tc.port)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ToUint16Port(%d) = %d, nil; want an error", tc.port, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ToUint16Port(%d) returned unexpected error: %v", tc.port, err)
			}
			if got != tc.want {
				t.Errorf("ToUint16Port(%d) = %d, want %d", tc.port, got, tc.want)
			}
		})
	}
}

func TestNetmapChanged(t *testing.T) {
	cases := []struct {
		name string
		n    ipn.Notify
		want bool
	}{
		{name: "empty notify", n: ipn.Notify{}, want: false},
		{name: "initial status", n: ipn.Notify{InitialStatus: &ipnstate.Status{}}, want: true},
		{name: "self change", n: ipn.Notify{SelfChange: &tailcfg.Node{}}, want: true},
		{name: "peers changed", n: ipn.Notify{PeersChanged: []*tailcfg.Node{{}}}, want: true},
		{name: "peers removed", n: ipn.Notify{PeersRemoved: []tailcfg.NodeID{1}}, want: true},
		{
			name: "peer changed patch",
			n:    ipn.Notify{PeerChangedPatch: []*tailcfg.PeerChange{{}}},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := netmapChanged(tc.n); got != tc.want {
				t.Errorf("netmapChanged(%+v) = %v, want %v", tc.n, got, tc.want)
			}
		})
	}
}
