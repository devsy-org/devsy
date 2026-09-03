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

	started := make(chan struct{})
	release := make(chan struct{})

	var updateWG sync.WaitGroup
	updateWG.Go(func() {
		inFlight.Store(true)
		close(started)
		// Wait for cancellation, mirroring updateConfig's select on
		// ctx.Done()/time.After.
		<-updateCtx.Done()
		// Block here so the goroutine stays in flight while the test
		// confirms stopUpdateThenClose has not closed the connection yet.
		<-release
		inFlight.Store(false)
	})

	// Deterministically wait for the goroutine to start (rather than a
	// fixed sleep) before triggering shutdown.
	<-started

	done := make(chan struct{})
	go func() {
		stopUpdateThenClose(cancelUpdate, &updateWG, closer)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("stopUpdateThenClose returned while updateConfig goroutine was still in flight")
	case <-time.After(50 * time.Millisecond):
		// stopUpdateThenClose is correctly blocked on updateWG.Wait();
		// the goroutine is still in flight and Close must not have run.
	}
	if closer.closed.Load() {
		t.Fatal("sshClient closed before updateConfig goroutine finished")
	}

	// Unblock the goroutine and let it finish.
	close(release)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stopUpdateThenClose did not return after goroutine finished")
	}

	if !closer.closed.Load() {
		t.Fatal("expected sshClient to be closed")
	}
}
