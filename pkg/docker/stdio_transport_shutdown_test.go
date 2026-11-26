package docker

import (
	"context"
	"io"
	"sync"
	"time"
)

// pendingReadCloser models a synchronous pipe whose Close waits for Read.
type pendingReadCloser struct {
	io.ReadCloser
	started, returned         chan struct{}
	startedOnce, returnedOnce sync.Once
}

func (r *pendingReadCloser) Read(p []byte) (int, error) {
	r.startedOnce.Do(func() { close(r.started) })
	n, err := r.ReadCloser.Read(p)
	r.returnedOnce.Do(func() { close(r.returned) })
	return n, err
}

func (r *pendingReadCloser) Close() error {
	<-r.returned
	return r.ReadCloser.Close()
}

func (s *StdioTransportSuite) TestCloseKillsChildBeforeClosingPendingRead() {
	transport, rec := recordedTransport(s, helperHangMode)
	var stdout *pendingReadCloser
	transport.launch = func(spec launchSpec) (*launchedStdio, error) {
		launched, err := rec.launch(spec)
		if err != nil {
			return nil, err
		}
		stdout = &pendingReadCloser{
			ReadCloser: launched.stdout,
			started:    make(chan struct{}),
			returned:   make(chan struct{}),
		}
		launched.stdout = stdout
		return launched, nil
	}
	// Also release an old implementation stuck in the simulated pipe Close.
	s.T().Cleanup(func() {
		for _, proc := range rec.snapshot() {
			_ = proc.KillTree()
		}
	})

	conn, err := transport.Dial(context.Background())
	s.Require().NoError(err)
	select {
	case <-stdout.started:
	case <-time.After(boundedInterval):
		s.FailNow("stdout read did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- conn.Close() }()
	s.Require().NoError(s.receive(closed))
	rec.requireAllReaped(s, 1)
}
