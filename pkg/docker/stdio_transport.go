package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	defaultShutdownTimeout = 5 * time.Second
	stderrTailBytes        = 4096
)

// StdioTransport dials the daemon by running `docker system dial-stdio`, which
// bridges its stdin/stdout to the daemon endpoint resolved by the docker CLI
// (DOCKER_HOST, contexts, ssh://, npipe, ...). Using the CLI keeps the SDK
// client pointed at exactly the daemon the CLI would use.
type StdioTransport struct {
	Invocation *DockerInvocation
	// ShutdownTimeout bounds how long Close waits for the subprocess to exit
	// after its stdin is closed before killing it.
	ShutdownTimeout time.Duration
}

// NewStdioTransport returns a transport for invocation with default timeouts.
func NewStdioTransport(invocation *DockerInvocation) *StdioTransport {
	return &StdioTransport{Invocation: invocation, ShutdownTimeout: defaultShutdownTimeout}
}

// Dial starts a dial-stdio subprocess and returns a connection to its stdio.
// ctx only governs startup; the returned connection outlives it and the
// subprocess is released by Close.
func (t *StdioTransport) Dial(ctx context.Context) (net.Conn, error) {
	if err := t.Invocation.validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("dial docker stdio: %w", err)
	}

	shutdownTimeout := t.ShutdownTimeout
	if shutdownTimeout <= 0 {
		shutdownTimeout = defaultShutdownTimeout
	}

	// Deliberately not CommandContext: the process must outlive the dial ctx.
	//nolint:gosec // command comes from trusted provider config
	cmd := exec.Command(t.Invocation.DockerCmd, "system", "dial-stdio")
	cmd.Env = t.Invocation.Environ()
	cmd.WaitDelay = shutdownTimeout
	setProcessGroupAttrs(cmd)

	stderr := &tailBuffer{limit: stderrTailBytes}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open stdin of %q dial-stdio: %w", t.Invocation.DockerCmd, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open stdout of %q dial-stdio: %w", t.Invocation.DockerCmd, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf(
			"start %q system dial-stdio (requires docker CLI 18.09+): %w",
			t.Invocation.DockerCmd, err,
		)
	}

	return &stdioConn{
		cmd:             cmd,
		stdin:           stdin,
		stdout:          stdout,
		stderr:          stderr,
		shutdownTimeout: shutdownTimeout,
	}, nil
}

// DialContext adapts Dial to the signature of http.Transport.DialContext; the
// network and address are ignored because the CLI resolves the endpoint.
func (t *StdioTransport) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	return t.Dial(ctx)
}

// stdioConn is a net.Conn over a subprocess's stdin/stdout. Deadlines are not
// supported and are accepted as no-ops, matching docker/cli's commandConn.
type stdioConn struct {
	cmd             *exec.Cmd
	stdin           io.WriteCloser
	stdout          io.ReadCloser
	stderr          *tailBuffer
	shutdownTimeout time.Duration

	closeStdinOnce sync.Once
	closeOnce      sync.Once
	reapOnce       sync.Once
	waitErr        error
}

var _ net.Conn = (*stdioConn)(nil)

func (c *stdioConn) Read(p []byte) (int, error) {
	n, err := c.stdout.Read(p)
	if errors.Is(err, io.EOF) {
		if exitErr := c.reap(); exitErr != nil {
			return n, exitErr
		}
	}
	return n, err
}

func (c *stdioConn) Write(p []byte) (int, error) {
	return c.stdin.Write(p)
}

// CloseWrite half-closes the connection so the daemon sees EOF on the stream.
func (c *stdioConn) CloseWrite() error {
	var err error
	c.closeStdinOnce.Do(func() { err = c.stdin.Close() })
	return err
}

// Close ends the stream, giving the subprocess ShutdownTimeout to exit before
// killing it.
func (c *stdioConn) Close() error {
	c.closeOnce.Do(func() {
		_ = c.CloseWrite()
		exited := make(chan struct{})
		go func() {
			_ = c.reap()
			close(exited)
		}()
		select {
		case <-exited:
		case <-time.After(c.shutdownTimeout):
			_ = killProcessGroup(c.cmd)
			<-exited
		}
	})
	return nil
}

func (c *stdioConn) LocalAddr() net.Addr  { return stdioAddr{} }
func (c *stdioConn) RemoteAddr() net.Addr { return stdioAddr{} }

func (c *stdioConn) SetDeadline(time.Time) error      { return nil }
func (c *stdioConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stdioConn) SetWriteDeadline(time.Time) error { return nil }

// reap waits for the subprocess exactly once and reports a non-zero exit with
// the tail of its stderr.
func (c *stdioConn) reap() error {
	c.reapOnce.Do(func() {
		err := c.cmd.Wait()
		if err == nil {
			return
		}
		c.waitErr = fmt.Errorf("docker system dial-stdio failed: %w", err)
		if tail := strings.TrimSpace(c.stderr.String()); tail != "" {
			c.waitErr = fmt.Errorf("%w: %s", c.waitErr, tail)
		}
	})
	return c.waitErr
}

type stdioAddr struct{}

func (stdioAddr) Network() string { return "stdio" }
func (stdioAddr) String() string  { return "docker-system-dial-stdio" }

// tailBuffer keeps the last limit bytes written to it.
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
