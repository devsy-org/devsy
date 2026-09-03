package tunnel

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

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
		<-updateCtx.Done()
		<-release
		inFlight.Store(false)
	})

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
		// stopUpdateThenClose is blocked on updateWG.Wait(); the goroutine
		// is still in flight and Close must not have run.
	}
	if closer.closed.Load() {
		t.Fatal("sshClient closed before updateConfig goroutine finished")
	}

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
