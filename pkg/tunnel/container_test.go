package tunnel

import (
	"bytes"
	"context"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

type closeBuffer struct {
	bytes.Buffer
	closed bool
}

func (b *closeBuffer) Close() error {
	b.closed = true
	return nil
}

func TestInternalProcessStderrPreservesNonLogOutput(t *testing.T) {
	fallback := &closeBuffer{}
	writer, closeAndDrain := internalProcessStderr(fallback)

	errorEnvelope := `{"kind":"error","outcome":"error","code":"UNKNOWN","message":` +
		`"container exec session produced no output for 30s: remote process never started"}`
	_, err := io.WriteString(writer,
		`{"level":"debug","msg":"structured-log-marker"}`+"\n"+
			errorEnvelope+"\n"+
			"plain subprocess failure\n")
	require.NoError(t, err)
	closeAndDrain()

	got := fallback.String()
	assert.NotContains(t, got, "structured-log-marker")
	assert.Contains(t, got, errorEnvelope)
	assert.Contains(t, got, "plain subprocess failure\n")
	assert.Equal(
		t,
		1,
		bytes.Count([]byte(got), []byte("container exec session produced no output")),
	)
	assert.True(t, fallback.closed)
}

func TestInternalProcessStderrDrainsFinalUnterminatedLine(t *testing.T) {
	fallback := &closeBuffer{}
	writer, closeAndDrain := internalProcessStderr(fallback)

	_, err := io.WriteString(writer,
		`{"kind":"error","message":"container exec session produced no output"}`)
	require.NoError(t, err)
	closeAndDrain()

	assert.Equal(t,
		`{"kind":"error","message":"container exec session produced no output"}`+"\n",
		fallback.String())
	assert.True(t, fallback.closed)
}
