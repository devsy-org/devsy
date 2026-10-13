package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultShutdownTimeout = 5 * time.Second
	exitDiagnosticWait     = time.Second
	stderrTailBytes        = 4096
)

var sensitiveEnvKey = regexp.MustCompile(`(?i)pass|secret|token|key`)

type StdioTransport struct {
	Invocation      *DockerInvocation
	ShutdownTimeout time.Duration

	mu    sync.Mutex
	conns map[*stdioConn]struct{}
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

	conn, err := t.spawn()
	if err != nil {
		return nil, err
	}
	conn.onClose = func() { t.untrack(conn) }
	t.track(conn)

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

// Close tears down every connection still owned by the transport.
func (t *StdioTransport) Close() error {
	t.mu.Lock()
	conns := make([]*stdioConn, 0, len(t.conns))
	for c := range t.conns {
		conns = append(conns, c)
	}
	t.mu.Unlock()

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

	// Deliberately not CommandContext: the process must outlive the dial ctx.
	//nolint:gosec // command comes from trusted provider config
	cmd := exec.Command(t.Invocation.DockerCmd, "system", "dial-stdio")
	cmd.Env = t.Invocation.Environ()
	setProcessGroupAttrs(cmd)

	stderr := &tailBuffer{limit: stderrTailBytes}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open stdin of %q dial-stdio: %w", t.Invocation.DockerCmd, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open stdout of %q dial-stdio: %w", t.Invocation.DockerCmd, err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf(
			"start %q system dial-stdio (requires docker CLI 18.09+): %w",
			t.Invocation.DockerCmd, err,
		)
	}

	return newStdioConn(stdioProc{
		cmd:             cmd,
		stdin:           stdin,
		stdout:          stdout,
		stderr:          stderr,
		shutdownTimeout: shutdownTimeout,
		redact:          t.Invocation.secretValues(),
	}), nil
}

func (t *StdioTransport) track(c *stdioConn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.conns == nil {
		t.conns = make(map[*stdioConn]struct{})
	}
	t.conns[c] = struct{}{}
}

func (t *StdioTransport) untrack(c *stdioConn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.conns, c)
}

func (i *DockerInvocation) secretValues() []string {
	var values []string
	for k, v := range i.Env {
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
	cmd             *exec.Cmd
	tree            *processTree
	stdin           io.WriteCloser
	stdout          io.ReadCloser
	stderr          *tailBuffer
	redact          []string
	shutdownTimeout time.Duration
	onClose         func()

	appRead, readPump   net.Conn
	appWrite, writePump net.Conn

	stdoutDone chan struct{}
	exited     chan struct{}
	waitErr    error

	closed         atomic.Bool
	closeOnce      sync.Once
	closeErr       error
	closeWriteOnce sync.Once
	closeWriteErr  error
}

var _ net.Conn = (*stdioConn)(nil)

type stdioProc struct {
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	stdout          io.ReadCloser
	stderr          *tailBuffer
	shutdownTimeout time.Duration
	redact          []string
}

func newStdioConn(p stdioProc) *stdioConn {
	c := &stdioConn{
		cmd:             p.cmd,
		tree:            attachProcessTree(p.cmd),
		stdin:           p.stdin,
		stdout:          p.stdout,
		stderr:          p.stderr,
		redact:          p.redact,
		shutdownTimeout: p.shutdownTimeout,
		stdoutDone:      make(chan struct{}),
		exited:          make(chan struct{}),
	}
	c.appRead, c.readPump = net.Pipe()
	c.appWrite, c.writePump = net.Pipe()

	go c.pumpStdout()
	go c.pumpStdin()
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
	_, _ = io.Copy(c.readPump, c.stdout)
	_ = c.readPump.Close()
}

func (c *stdioConn) pumpStdin() {
	_, _ = io.Copy(c.stdin, c.writePump)
	_ = c.stdin.Close()
	_ = c.writePump.Close()
}

// reap is the only caller of cmd.Wait. os/exec requires stdout reads to finish
// before Wait, and a child that closes stdout but lingers is killed after the
// shutdown timeout so it can never pin the connection.
func (c *stdioConn) reap() {
	<-c.stdoutDone

	lingering := time.AfterFunc(c.shutdownTimeout, func() { _ = c.tree.kill() })
	err := c.cmd.Wait()
	lingering.Stop()

	c.waitErr = c.describeExit(err)
	c.tree.release()
	close(c.exited)
}

func (c *stdioConn) describeExit(err error) error {
	if err == nil {
		return nil
	}
	described := fmt.Errorf("docker system dial-stdio failed: %w", err)
	if tail := strings.TrimSpace(c.redactSecrets(c.stderr.String())); tail != "" {
		described = fmt.Errorf("%w: %s", described, tail)
	}
	return described
}

func (c *stdioConn) redactSecrets(s string) string {
	for _, secret := range c.redact {
		s = strings.ReplaceAll(s, secret, "***")
	}
	return s
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
	_ = c.stdin.Close()
	_ = c.stdout.Close()

	if c.waitExited(c.shutdownTimeout) {
		return nil
	}
	killErr := c.tree.kill()
	if c.waitExited(c.shutdownTimeout) {
		return nil
	}
	return errors.Join(
		fmt.Errorf(
			"docker system dial-stdio (pid %d) did not exit within %s of being killed",
			c.cmd.Process.Pid, c.shutdownTimeout,
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
