//go:build !windows

package channel

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy-signals/envelope"
	"github.com/devsy-org/devsy/pkg/pool/audit"
	"github.com/devsy-org/devsy/pkg/pool/egress/events"
	"github.com/stretchr/testify/require"
)

const (
	orderEvents  = 30
	orderSession = "p-test-pool-abcdefghij"
)

type auditStub struct {
	mu     sync.Mutex
	events []audit.Event
}

func (a *auditStub) Write(e audit.Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	return nil
}

func (a *auditStub) count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.events)
}

type recorderRun struct {
	rec    *events.Recorder
	audit  *auditStub
	cancel context.CancelFunc
	done   chan error
}

func startRecorder(t *testing.T) *recorderRun {
	t.Helper()
	sink := &auditStub{}
	rec, err := events.New(events.Config{
		Sink:        sink,
		Session:     orderSession,
		Actor:       envelope.Actor{Kind: envelope.ActorAgent, ID: "claude"},
		Interval:    time.Hour,
		ShowAddress: func(string) bool { return true },
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	run := &recorderRun{rec: rec, audit: sink, cancel: cancel, done: make(chan error, 1)}
	go func() { run.done <- rec.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-run.done
		run.done <- nil
	})
	return run
}

func (r *recorderRun) stop(t *testing.T) {
	t.Helper()
	r.cancel()
	select {
	case err := <-r.done:
		require.NoError(t, err)
		r.done <- nil
	case <-time.After(waitFor):
		t.Fatal("the recorder did not stop")
	}
}

func sendAllows(t *testing.T, path string) {
	t.Helper()
	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	for i := range orderEvents {
		line := fmt.Sprintf(`{"t":"allow","host":"h%d.example.com","port":443}`+"\n", i)
		_, err := conn.Write([]byte(line))
		require.NoError(t, err)
	}
}

// Stopping the server first drains what the sidecar sent into the recorder,
// and the recorder's final flush then writes all of it.
func (s *ServerSuite) TestStoppingTheServerBeforeTheRecorderLosesNothing() {
	run := startRecorder(s.T())
	env := startServerFor(s.T(), filepath.Join(s.dir, "a.sock"), run.rec)
	sendAllows(s.T(), env.path)
	env.stop()
	run.stop(s.T())

	s.Equal(orderEvents, run.audit.count())
	s.Zero(run.rec.Stats().Late)
}

// Stopping the recorder first leaves later decisions with nowhere to go. The
// recorder counts them, so the order is the only thing that keeps them.
func (s *ServerSuite) TestStoppingTheRecorderBeforeTheServerCountsWhatItMissed() {
	run := startRecorder(s.T())
	env := startServerFor(s.T(), filepath.Join(s.dir, "a.sock"), run.rec)
	run.stop(s.T())
	sendAllows(s.T(), env.path)
	// The server must have read the lines before it stops, or a slow
	// scheduler leaves them in a connection nobody accepted.
	s.Eventually(func() bool { return env.server.Stats().Events == orderEvents },
		waitFor, waitTick)
	env.stop()

	s.Zero(run.audit.count())
	s.Equal(orderEvents, run.rec.Stats().Late)
}
