package docker

import (
	"bufio"
	"context"
	"errors"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// recordedProc wraps a real dialProcess and records its lifecycle so tests can
// assert actual Wait completion rather than an empty tracking map.
type recordedProc struct {
	dialProcess

	waited   chan struct{}
	waitOnce sync.Once
	waits    atomic.Int32
	releases atomic.Int32
}

func (p *recordedProc) Wait() error {
	p.waits.Add(1)
	err := p.dialProcess.Wait()
	p.waitOnce.Do(func() { close(p.waited) })
	return err
}

func (p *recordedProc) Release() error {
	p.releases.Add(1)
	return p.dialProcess.Release()
}

// launchRecorder is the injected spawn seam. pre runs after admission and
// before the real launch (a non-nil error aborts the launch); post runs after
// the real launch and before the child is handed back to the transport.
type launchRecorder struct {
	launches atomic.Int32
	pre      func(n int) error
	post     func(n int)

	mu    sync.Mutex
	procs []*recordedProc
}

func (r *launchRecorder) launch(spec launchSpec) (*launchedStdio, error) {
	n := int(r.launches.Add(1))
	if r.pre != nil {
		if err := r.pre(n); err != nil {
			return nil, err
		}
	}
	launched, err := launchDialStdio(spec)
	if err != nil {
		return nil, err
	}
	rec := &recordedProc{dialProcess: launched.proc, waited: make(chan struct{})}
	launched.proc = rec
	r.mu.Lock()
	r.procs = append(r.procs, rec)
	r.mu.Unlock()
	if r.post != nil {
		r.post(n)
	}
	return launched, nil
}

func (r *launchRecorder) snapshot() []*recordedProc {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*recordedProc(nil), r.procs...)
}

// requireAllReaped asserts every launched child was waited on exactly once and
// its ownership released exactly once.
func (r *launchRecorder) requireAllReaped(s *StdioTransportSuite, want int) {
	s.T().Helper()
	procs := r.snapshot()
	s.Require().Len(procs, want)
	for _, p := range procs {
		select {
		case <-p.waited:
		default:
			s.Failf("child not reaped", "pid %d still running", p.PID())
		}
		s.EqualValues(1, p.waits.Load(), "pid %d must have exactly one waiter", p.PID())
		s.EqualValues(1, p.releases.Load(), "pid %d must be released exactly once", p.PID())
	}
}

type dialResult struct {
	conn any
	err  error
}

func recordedTransport(s *StdioTransportSuite, mode string) (*StdioTransport, *launchRecorder) {
	rec := &launchRecorder{}
	transport := NewStdioTransport(helperInvocation(s.T(), mode))
	transport.ShutdownTimeout = shortShutdown
	transport.launch = rec.launch
	s.T().Cleanup(func() { _ = transport.Close() })
	return transport, rec
}

func transportState(t *StdioTransport) (closed bool, starting, conns int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed, t.starting, len(t.conns)
}

func (s *StdioTransportSuite) requireIdle(t *StdioTransport) {
	s.T().Helper()
	closed, starting, conns := transportState(t)
	s.True(closed)
	s.Zero(starting, "no admitted startup may remain")
	s.Zero(conns, "no connection may remain tracked")
}

// waitUntil spins on observable state; it never sleeps to "let things happen".
func (s *StdioTransportSuite) waitUntil(cond func() bool) {
	s.T().Helper()
	deadline := time.Now().Add(boundedInterval)
	for !cond() {
		if time.Now().After(deadline) {
			s.FailNow("condition not reached")
		}
		runtime.Gosched()
	}
}

func (s *StdioTransportSuite) requireNotDone(done <-chan error, msg string) {
	s.T().Helper()
	select {
	case <-done:
		s.FailNow(msg)
	default:
	}
}

func (s *StdioTransportSuite) receive(done <-chan error) error {
	s.T().Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(boundedInterval):
		s.FailNow("operation did not complete")
		return nil
	}
}

func (s *StdioTransportSuite) TestCloseWaitsForInFlightSpawn() {
	transport, rec := recordedTransport(s, "hang")
	launched := make(chan struct{})
	release := make(chan struct{})
	rec.post = func(int) {
		close(launched)
		<-release
	}

	dialDone := make(chan dialResult, 1)
	go func() {
		conn, err := transport.Dial(context.Background())
		dialDone <- dialResult{conn: conn, err: err}
	}()
	<-launched

	closeDone := make(chan error, 1)
	go func() { closeDone <- transport.Close() }()
	s.waitUntil(func() bool { closed, _, _ := transportState(transport); return closed })

	// closed is set and the admitted startup is still in flight, so Close must
	// be parked on the condition variable.
	s.requireNotDone(closeDone, "Close returned while a launch was in flight")
	_, starting, _ := transportState(transport)
	s.Equal(1, starting)

	close(release)
	res := <-dialDone
	s.Nil(res.conn, "a connection that lost the race with Close must not be usable")
	s.Require().ErrorIs(res.err, errTransportClosed)
	s.Require().NoError(s.receive(closeDone))

	rec.requireAllReaped(s, 1)
	s.requireIdle(transport)
}

func (s *StdioTransportSuite) TestCloseAfterPublishedDial() {
	transport, rec := recordedTransport(s, "hang")
	conn, err := transport.Dial(context.Background())
	s.Require().NoError(err)
	_, _, conns := transportState(transport)
	s.Equal(1, conns)

	readDone := make(chan error, 1)
	go func() {
		_, err := conn.Read(make([]byte, 1))
		readDone <- err
	}()

	s.Require().NoError(transport.Close())
	s.Require().Error(s.receive(readDone), "blocked read must be released by Close")

	rec.requireAllReaped(s, 1)
	s.requireIdle(transport)
}

func (s *StdioTransportSuite) TestDialAfterClose() {
	transport, rec := recordedTransport(s, "echo")
	s.Require().NoError(transport.Close())

	conn, err := transport.Dial(context.Background())
	s.Nil(conn)
	s.Require().ErrorIs(err, errTransportClosed)
	s.Zero(rec.launches.Load(), "a rejected dial must not start a process")
	s.requireIdle(transport)
}

func (s *StdioTransportSuite) TestConcurrentClose() {
	const dials, closers = 3, 16
	transport, rec := recordedTransport(s, "hang")
	for range dials {
		_, err := transport.Dial(context.Background())
		s.Require().NoError(err)
	}

	start := make(chan struct{})
	errs := make([]error, closers)
	var wg sync.WaitGroup
	for i := range closers {
		wg.Go(func() {
			<-start
			errs[i] = transport.Close()
		})
	}
	close(start)
	wg.Wait()

	for _, err := range errs {
		s.Equal(errs[0], err, "every Close caller observes the same outcome")
	}
	s.Require().NoError(errs[0])
	rec.requireAllReaped(s, dials) // exactly one waiter and release per child
	s.requireIdle(transport)
}

func (s *StdioTransportSuite) TestSpawnErrorDuringClose() {
	errInjected := errors.New("injected launch failure")
	transport, rec := recordedTransport(s, "echo")
	entered := make(chan struct{})
	release := make(chan struct{})
	rec.pre = func(int) error {
		close(entered)
		<-release
		return errInjected
	}

	dialDone := make(chan dialResult, 1)
	go func() {
		conn, err := transport.Dial(context.Background())
		dialDone <- dialResult{conn: conn, err: err}
	}()
	<-entered

	closeDone := make(chan error, 1)
	go func() { closeDone <- transport.Close() }()
	s.waitUntil(func() bool { closed, _, _ := transportState(transport); return closed })
	s.requireNotDone(closeDone, "Close returned while a launch was in flight")

	close(release)
	res := <-dialDone
	s.Nil(res.conn)
	s.Require().ErrorIs(res.err, errInjected)
	s.Require().NoError(s.receive(closeDone))

	rec.requireAllReaped(s, 0)
	s.requireIdle(transport)
}

func (s *StdioTransportSuite) TestHijackCancelDoesNotAffectSibling() {
	client := newDaemonClient(s)
	rec := &launchRecorder{}
	pendingCtx, cancelPending := context.WithCancel(context.Background())
	defer cancelPending()
	// Cancel the pending negotiation as soon as its child is launched; the
	// abort lands either before or after the connection registers, and both
	// orders must clean up only that connection.
	rec.post = func(n int) {
		if n == 2 {
			cancelPending()
		}
	}
	client.stdio.launch = rec.launch

	healthy, err := client.DialHijack(context.Background(), "/session", "tcp", nil)
	s.Require().NoError(err)
	defer func() { _ = healthy.Close() }()
	echoThrough(s, healthy, "before")

	_, err = client.DialHijack(pendingCtx, "/stuck", "tcp", nil)
	s.Require().ErrorIs(err, context.Canceled)

	echoThrough(s, healthy, "after")
	procs := rec.snapshot()
	s.Require().Len(procs, 2)
	select {
	case <-procs[1].waited:
	case <-time.After(boundedInterval):
		s.Fail("aborted attempt's child was not reaped")
	}
	select {
	case <-procs[0].waited:
		s.Fail("sibling's child must still be running")
	default:
	}
}

func (s *StdioTransportSuite) TestCloseWithManyDials() {
	const dials = 12
	transport, rec := recordedTransport(s, "echo")

	start := make(chan struct{})
	results := make([]dialResult, dials)
	var wg sync.WaitGroup
	for i := range dials {
		wg.Go(func() {
			<-start
			conn, err := transport.Dial(context.Background())
			results[i] = dialResult{conn: conn, err: err}
		})
	}
	closeDone := make(chan error, 1)
	go func() {
		<-start
		closeDone <- transport.Close()
	}()
	close(start)
	wg.Wait()
	s.Require().NoError(s.receive(closeDone))

	var published int
	for _, res := range results {
		if res.err != nil {
			s.Require().ErrorIs(res.err, errTransportClosed)
			s.Nil(res.conn)
			continue
		}
		published++
	}
	// Every launched child, published or rejected after launch, must be reaped.
	launched := len(rec.snapshot())
	s.GreaterOrEqual(launched, published)
	s.EqualValues(launched, rec.launches.Load())
	rec.requireAllReaped(s, launched)
	s.requireIdle(transport)
}

// TestCloseKillsImmediateChildDescendants proves the whole tree dies: the
// dial-stdio child starts a descendant at once (the window that used to escape
// the Windows job) and both must be gone after Close.
func (s *StdioTransportSuite) TestCloseKillsImmediateChildDescendants() {
	transport, rec := recordedTransport(s, "spawn-child")
	conn, err := transport.Dial(context.Background())
	s.Require().NoError(err)
	s.Require().NoError(conn.SetReadDeadline(time.Now().Add(30 * time.Second)))

	line, err := bufio.NewReader(conn).ReadString('\n')
	s.Require().NoError(err)
	descendant, err := strconv.Atoi(strings.TrimSpace(line))
	s.Require().NoError(err)
	procs := rec.snapshot()
	s.Require().Len(procs, 1)
	parent := procs[0].PID()
	s.True(processRunning(parent), "child must exist before Close")
	s.True(processRunning(descendant), "descendant must exist before Close")

	s.Require().NoError(transport.Close())

	s.Eventually(func() bool { return !processRunning(parent) },
		boundedInterval, 10*time.Millisecond, "dial-stdio child survived Close")
	s.Eventually(func() bool { return !processRunning(descendant) },
		boundedInterval, 10*time.Millisecond, "descendant of dial-stdio survived Close")
	rec.requireAllReaped(s, 1)
}
