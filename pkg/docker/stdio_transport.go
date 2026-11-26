package docker

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultShutdownTimeout = 5 * time.Second
	killReapTimeout        = 5 * time.Second
	exitDiagnosticWait     = time.Second
	stderrTailBytes        = 4096
)

// errTransportClosed is returned (wrapped) by Dial once Close has begun.
var errTransportClosed = errors.New("docker stdio transport is closed")

var sensitiveEnvKey = regexp.MustCompile(`(?i)pass|secret|token|key`)

type StdioTransport struct {
	Invocation      *DockerInvocation
	ShutdownTimeout time.Duration

	// launch is the process-start seam; nil means launchDialStdio.
	launch dialLauncher

	mu       sync.Mutex
	cond     *sync.Cond // signalled when starting drops; lazily bound to mu
	closed   bool       // guarded by mu; set once by Close, never cleared
	starting int        // guarded by mu; admitted Dials not yet published or cleaned up
	conns    map[*stdioConn]struct{}

	closeOnce sync.Once
	closeErr  error // read only after closeOnce.Do returns
}

func NewStdioTransport(invocation *DockerInvocation) *StdioTransport {
	return &StdioTransport{Invocation: invocation, ShutdownTimeout: defaultShutdownTimeout}
}

func (t *StdioTransport) Dial(ctx context.Context) (net.Conn, error) {
	if err := t.Invocation.validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("dial docker stdio: %w", err)
	}

	conn, err := t.open()
	if err != nil {
		return nil, err
	}
	if attempt, ok := ctx.Value(hijackAttemptKey{}).(*hijackAttempt); ok &&
		!attempt.register(conn) {
		_ = conn.Close()
		return nil, fmt.Errorf("dial docker stdio: %w", context.Cause(ctx))
	}
	return conn, nil
}

func (t *StdioTransport) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	return t.Dial(ctx)
}

// Close rejects further dials, waits for every admitted startup to be
// published or cleaned up, then closes every tracked connection and returns
// after their children are reaped. All callers observe the same outcome.
func (t *StdioTransport) Close() error {
	t.closeOnce.Do(func() { t.closeErr = t.shutdown() })
	return t.closeErr
}

// open admits one startup, launches the child outside the lock and publishes
// the connection, or cleans the child up before retiring the admission.
func (t *StdioTransport) open() (*stdioConn, error) {
	if err := t.beginDial(); err != nil {
		return nil, fmt.Errorf("dial docker stdio: %w", err)
	}

	// Exactly one path retires the admission: publish on success, otherwise
	// this deferred call after any child cleanup has finished.
	published := false
	defer func() {
		if !published {
			t.finishStarting()
		}
	}()

	conn, err := t.spawn()
	if err != nil {
		return nil, err
	}
	if !t.publish(conn) {
		return nil, errors.Join(
			fmt.Errorf("dial docker stdio: %w", errTransportClosed),
			conn.Close(), // never published: reap it before Close can return
		)
	}
	published = true
	return conn, nil
}

func (t *StdioTransport) shutdown() error {
	t.mu.Lock()
	t.closed = true
	for t.starting > 0 {
		t.startupCond().Wait()
	}
	conns := make([]*stdioConn, 0, len(t.conns))
	for c := range t.conns {
		conns = append(conns, c)
	}
	t.mu.Unlock()

	// Not under mu: conn.Close -> onClose -> untrack takes it.
	errs := make([]error, len(conns))
	var wg sync.WaitGroup
	for i, c := range conns {
		wg.Go(func() { errs[i] = c.Close() })
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (t *StdioTransport) spawn() (*stdioConn, error) {
	shutdownTimeout := t.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultShutdownTimeout
	}
	launch := t.launch
	if launch == nil {
		launch = launchDialStdio
	}

	launched, err := launch(t.Invocation.launchSpec())
	if err != nil {
		return nil, fmt.Errorf(
			"start %q system dial-stdio (requires docker CLI 18.09+): %w",
			t.Invocation.DockerCmd, err,
		)
	}
	return newStdioConn(stdioProc{
		launched:        launched,
		shutdownTimeout: shutdownTimeout,
		redact:          t.Invocation.secretValues(),
	}), nil
}

// beginDial admits one Dial or rejects it once the transport is closed. Every
// admission must be matched by exactly one finishStarting (or publish).
func (t *StdioTransport) beginDial() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return errTransportClosed
	}
	t.starting++
	return nil
}

// finishStarting retires one admission and wakes a Close waiting on startups.
func (t *StdioTransport) finishStarting() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.starting--
	t.startupCond().Broadcast()
}

// publish is the successful dial's linearization point: it tracks c and
// retires the admission, or reports false if Close already won, in which case
// the caller owns c's cleanup and must finishStarting afterwards.
func (t *StdioTransport) publish(c *stdioConn) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	c.onClose = func() { t.untrack(c) }
	if t.conns == nil {
		t.conns = make(map[*stdioConn]struct{})
	}
	t.conns[c] = struct{}{}
	t.starting--
	t.startupCond().Broadcast()
	return true
}

func (t *StdioTransport) untrack(c *stdioConn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.conns, c)
}

// startupCond returns the startup condition variable. t.mu must be held.
func (t *StdioTransport) startupCond() *sync.Cond {
	if t.cond == nil {
		t.cond = sync.NewCond(&t.mu)
	}
	return t.cond
}

func (i *DockerInvocation) secretValues() []string {
	var values []string
	for k, v := range i.env {
		if v == "" {
			continue
		}
		if sensitiveEnvKey.MatchString(k) {
			values = append(values, v)
		}
		if u, err := url.Parse(v); err == nil && u.User != nil {
			if pw, ok := u.User.Password(); ok && pw != "" {
				values = append(values, pw)
			}
		}
	}
	return values
}

// stdioConn bridges a child's stdio to a net.Conn through two independent
// net.Pipe pairs, so each direction has real deadlines and can be half-closed.
type stdioConn struct {
	proc            dialProcess
	stdin           io.WriteCloser
	stdout          io.ReadCloser
	stderr          io.ReadCloser
	stderrTail      *tailBuffer
	redact          []string
	shutdownTimeout time.Duration
	onClose         func()

	appRead, readPump   net.Conn
	appWrite, writePump net.Conn

	stdinDone  chan struct{}
	stdoutDone chan struct{}
	stderrDone chan struct{}
	exited     chan struct{}
	waitErr    error // written before exited closes
	releaseErr error // written before exited closes

	closed         atomic.Bool
	closeOnce      sync.Once
	closeErr       error
	closeWriteOnce sync.Once
	closeWriteErr  error
}

var _ net.Conn = (*stdioConn)(nil)

type stdioProc struct {
	launched        *launchedStdio
	shutdownTimeout time.Duration
	redact          []string
}

func newStdioConn(p stdioProc) *stdioConn {
	c := &stdioConn{
		proc:            p.launched.proc,
		stdin:           p.launched.stdin,
		stdout:          p.launched.stdout,
		stderr:          p.launched.stderr,
		stderrTail:      &tailBuffer{limit: stderrTailBytes},
		redact:          p.redact,
		shutdownTimeout: p.shutdownTimeout,
		stdinDone:       make(chan struct{}),
		stdoutDone:      make(chan struct{}),
		stderrDone:      make(chan struct{}),
		exited:          make(chan struct{}),
	}
	c.appRead, c.readPump = net.Pipe()
	c.appWrite, c.writePump = net.Pipe()

	go c.pumpStdout()
	go c.pumpStdin()
	go c.pumpStderr()
	go c.reap()
	return c
}

func (c *stdioConn) Read(p []byte) (int, error) {
	n, err := c.appRead.Read(p)
	switch {
	case err == nil:
		return n, nil
	case c.closed.Load() && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe)):
		return n, net.ErrClosed
	case errors.Is(err, io.EOF):
		if c.waitExited(min(c.shutdownTimeout, exitDiagnosticWait)) && c.waitErr != nil {
			return n, c.waitErr
		}
	}
	return n, err
}

func (c *stdioConn) Write(p []byte) (int, error) {
	n, err := c.appWrite.Write(p)
	if err != nil && c.closed.Load() && errors.Is(err, io.ErrClosedPipe) {
		return n, net.ErrClosed
	}
	return n, err
}

func (c *stdioConn) CloseWrite() error {
	c.closeWriteOnce.Do(func() { c.closeWriteErr = c.appWrite.Close() })
	return c.closeWriteErr
}

func (c *stdioConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.shutdown()
		if c.onClose != nil {
			c.onClose()
		}
	})
	return c.closeErr
}

func (c *stdioConn) LocalAddr() net.Addr  { return stdioAddr{} }
func (c *stdioConn) RemoteAddr() net.Addr { return stdioAddr{} }

func (c *stdioConn) SetDeadline(t time.Time) error {
	return errors.Join(c.appRead.SetReadDeadline(t), c.appWrite.SetWriteDeadline(t))
}

func (c *stdioConn) SetReadDeadline(t time.Time) error  { return c.appRead.SetReadDeadline(t) }
func (c *stdioConn) SetWriteDeadline(t time.Time) error { return c.appWrite.SetWriteDeadline(t) }

func (c *stdioConn) pumpStdout() {
	defer close(c.stdoutDone)
	defer func() { _ = c.stdout.Close() }()
	_, _ = io.Copy(c.readPump, c.stdout)
	_ = c.readPump.Close()
}

func (c *stdioConn) pumpStdin() {
	defer close(c.stdinDone)
	_, _ = io.Copy(c.stdin, c.writePump)
	_ = c.stdin.Close()
	_ = c.writePump.Close()
}

func (c *stdioConn) pumpStderr() {
	defer close(c.stderrDone)
	defer func() { _ = c.stderr.Close() }()
	_, _ = io.Copy(c.stderrTail, c.stderr)
}

// reap is the only caller of proc.Wait. A child that closes stdout but lingers
// is killed after the shutdown timeout so it can never pin the connection.
// Once the child is gone, Release closes the ownership handles (which also
// reaps any straggling descendant) and the stderr drain is given a bounded
// moment to finish so the diagnostic tail is complete.
func (c *stdioConn) reap() {
	<-c.stdoutDone

	lingering := time.AfterFunc(c.shutdownTimeout, func() { _ = c.proc.KillTree() })
	err := c.proc.Wait()
	lingering.Stop()
	c.releaseErr = c.proc.Release()
	_ = c.writePump.Close()
	<-c.stdinDone

	drain := time.NewTimer(min(c.shutdownTimeout, exitDiagnosticWait))
	select {
	case <-c.stderrDone:
	case <-drain.C:
		_ = c.stderr.Close()
		<-c.stderrDone
	}
	drain.Stop()

	c.waitErr = c.describeExit(err)
	close(c.exited)
}

func (c *stdioConn) describeExit(err error) error {
	if err == nil {
		return nil
	}
	described := fmt.Errorf("docker system dial-stdio failed: %w", err)
	if tail := strings.TrimSpace(c.redactSecrets(c.stderrTail.String())); tail != "" {
		described = fmt.Errorf("%w: %s", described, tail)
	}
	return described
}

func (c *stdioConn) redactSecrets(s string) string {
	secrets := slices.Clone(c.redact)
	// A shorter credential may otherwise leave a longer credential's suffix visible.
	slices.SortFunc(secrets, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	pairs := make([]string, 0, len(secrets)*2)
	for _, secret := range secrets {
		pairs = append(pairs, secret, "***")
	}
	return strings.NewReplacer(pairs...).Replace(s)
}

func (c *stdioConn) waitExited(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-c.exited:
		return true
	case <-timer.C:
		return false
	}
}

// shutdown unblocks all caller I/O immediately, gives the child a bounded
// chance to exit on stdin EOF, then kills the owned process tree.
func (c *stdioConn) shutdown() error {
	c.closed.Store(true)
	for _, pipe := range []net.Conn{c.appRead, c.appWrite, c.readPump, c.writePump} {
		_ = pipe.Close()
	}
	// OS pipe Close can wait for synchronous I/O on Windows. Each pump owns
	// that close; keep this path free to reach the process-kill deadline.
	if c.waitExited(c.shutdownTimeout) {
		return c.releaseErr
	}
	killErr := c.proc.KillTree()
	// A killed tree must exit regardless of the grace period; reaping also
	// covers Release's process-table scan and the stderr drain.
	if c.waitExited(killReapTimeout) {
		return c.releaseErr
	}
	return errors.Join(
		fmt.Errorf(
			"docker system dial-stdio (pid %d) did not exit within %s of being killed",
			c.proc.PID(), killReapTimeout,
		),
		killErr,
	)
}

type stdioAddr struct{}

func (stdioAddr) Network() string { return "stdio" }
func (stdioAddr) String() string  { return "docker-system-dial-stdio" }

type tailBuffer struct {
	mu    sync.Mutex
	limit int
	buf   []byte
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if excess := len(b.buf) - b.limit; excess > 0 {
		b.buf = b.buf[excess:]
	}
	return len(p), nil
}

func (b *tailBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
