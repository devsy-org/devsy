package tunnel

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeCloser records whether Close was called and, if inFlight is set,
// verifies it is only called after any in-flight work has finished.
type fakeCloser struct {
	closed   atomic.Bool
	inFlight *atomic.Bool
}

func (f *fakeCloser) Close() error {
	f.closed.Store(true)
	if f.inFlight != nil && f.inFlight.Load() {
		panic("sshClient closed while updateConfig goroutine still running")
	}
	return nil
}

// TestStopUpdateThenCloseWaitsForGoroutine verifies that closing the shared
// connection is deferred until the periodic update-config goroutine has
// fully exited after cancellation, preventing a race where the goroutine
// uses a connection that has already been closed.
func TestStopUpdateThenCloseWaitsForGoroutine(t *testing.T) {
	updateCtx, cancelUpdate := context.WithCancel(context.Background())
	defer cancelUpdate()

	var inFlight atomic.Bool
	closer := &fakeCloser{inFlight: &inFlight}

	var updateWG sync.WaitGroup
	updateWG.Go(func() {
		inFlight.Store(true)
		// Simulate work that only checks for cancellation periodically,
		// mirroring updateConfig's select on ctx.Done()/time.After.
		select {
		case <-updateCtx.Done():
		case <-time.After(2 * time.Second):
		}
		// Simulate the goroutine still using the connection briefly after
		// observing cancellation, before it fully returns.
		time.Sleep(20 * time.Millisecond)
		inFlight.Store(false)
	})

	// Give the goroutine a moment to start and mark itself in-flight.
	time.Sleep(10 * time.Millisecond)

	stopUpdateThenClose(cancelUpdate, &updateWG, closer)

	if !closer.closed.Load() {
		t.Fatal("expected sshClient to be closed")
	}
}
