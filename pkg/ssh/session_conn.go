package ssh

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/transport"
	xssh "golang.org/x/crypto/ssh"
)

type SessionConnOptions struct {
	Command string
	Env     map[string]string
	Stderr  io.Writer
}

type sessionConn struct {
	stdin   io.WriteCloser
	stdout  io.Reader
	session *xssh.Session
	cancel  context.CancelFunc

	closeOnce sync.Once
	waitOnce  sync.Once
	waitDone  chan struct{}
	waitErr   error
}

// OpenSessionConn exposes a long-lived command on an existing SSH client as a
// managed connection. Closing it never closes the parent client.
func OpenSessionConn(
	ctx context.Context,
	client *xssh.Client,
	opts SessionConnOptions,
) (transport.ManagedConn, error) {
	if err := validateSessionConn(client, opts); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	session, err := startSession(client, opts)
	if err != nil {
		return nil, err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("create ssh session stdin: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("create ssh session stdout: %w", err)
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	conn := &sessionConn{
		stdin: stdin, stdout: stdout, session: session, cancel: cancel,
		waitDone: make(chan struct{}),
	}
	go func() {
		<-sessionCtx.Done()
		_ = conn.Close()
	}()
	return conn, nil
}

func validateSessionConn(client *xssh.Client, opts SessionConnOptions) error {
	if client == nil {
		return fmt.Errorf("ssh client is required")
	}
	if opts.Command == "" {
		return fmt.Errorf("command is required")
	}
	return nil
}

func startSession(client *xssh.Client, opts SessionConnOptions) (*xssh.Session, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("create ssh session: %w", err)
	}
	session.Stderr = opts.Stderr
	for key, value := range opts.Env {
		if err := session.Setenv(key, value); err != nil {
			_ = session.Close()
			return nil, fmt.Errorf("set ssh session environment %q: %w", key, err)
		}
	}
	if err := session.Start(opts.Command); err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("start ssh session: %w", err)
	}
	return session, nil
}

func (c *sessionConn) Read(p []byte) (int, error)  { return c.stdout.Read(p) }
func (c *sessionConn) Write(p []byte) (int, error) { return c.stdin.Write(p) }

func (c *sessionConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.cancel()
		if closeErr := c.stdin.Close(); closeErr != nil {
			err = closeErr
		}
		if closeErr := c.session.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	})
	return err
}

func (c *sessionConn) LocalAddr() net.Addr              { return transport.NewAddr("ssh-session") }
func (c *sessionConn) RemoteAddr() net.Addr             { return transport.NewAddr("ssh-session") }
func (c *sessionConn) SetDeadline(time.Time) error      { return os.ErrInvalid }
func (c *sessionConn) SetReadDeadline(time.Time) error  { return os.ErrInvalid }
func (c *sessionConn) SetWriteDeadline(time.Time) error { return os.ErrInvalid }
func (c *sessionConn) Wait() error {
	c.waitOnce.Do(func() {
		c.waitErr = c.session.Wait()
		c.cancel()
		close(c.waitDone)
	})
	<-c.waitDone
	return c.waitErr
}

var (
	_ net.Conn              = (*sessionConn)(nil)
	_ transport.ManagedConn = (*sessionConn)(nil)
)
