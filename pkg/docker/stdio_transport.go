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

type StdioTransport struct {
	Invocation *DockerInvocation
	ShutdownTimeout time.Duration
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

func (t *StdioTransport) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	return t.Dial(ctx)
}

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

func (c *stdioConn) CloseWrite() error {
	var err error
	c.closeStdinOnce.Do(func() { err = c.stdin.Close() })
	return err
}

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
