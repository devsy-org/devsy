package ssh

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/devsy-org/devsy/pkg/stdio"
	"golang.org/x/crypto/ssh"
)

const keepAliveRequestType = "keepalive@openssh.com"

func handleKeepAliveRequests(in <-chan *ssh.Request) <-chan *ssh.Request {
	out := make(chan *ssh.Request)
	go func() {
		defer close(out)
		for req := range in {
			if req.Type == keepAliveRequestType {
				if req.WantReply {
					_ = req.Reply(true, nil)
				}
				continue
			}
			out <- req
		}
	}()
	return out
}

func NewSSHPassClient(user, addr, password string) (*ssh.Client, error) {
	clientConfig := &ssh.ClientConfig{
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	clientConfig.Auth = append(clientConfig.Auth, ssh.Password(password))

	if user != "" {
		clientConfig.User = user
	}

	client, err := ssh.Dial("tcp", addr, clientConfig)
	if err != nil {
		return nil, fmt.Errorf("dial to %v failed: %w", addr, err)
	}

	return client, nil
}

func NewSSHClient(user, addr string, keyBytes []byte) (*ssh.Client, error) {
	sshConfig, err := ConfigFromKeyBytes(keyBytes)
	if err != nil {
		return nil, err
	}

	if user != "" {
		sshConfig.User = user
	}

	client, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("dial to %v failed: %w", addr, err)
	}

	return client, nil
}

func StdioClient(reader io.Reader, writer io.WriteCloser) (*ssh.Client, error) {
	return StdioClientFromKeyBytesWithUser(nil, reader, writer, "")
}

func StdioClientWithUser(
	reader io.Reader,
	writer io.WriteCloser,
	user string,
) (*ssh.Client, error) {
	return StdioClientFromKeyBytesWithUser(nil, reader, writer, user)
}

func StdioClientFromKeyBytesWithUser(
	keyBytes []byte,
	reader io.Reader,
	writer io.WriteCloser,
	user string,
) (*ssh.Client, error) {
	conn := stdio.NewStdioStream(reader, writer)
	return ClientFromConn(conn, user, keyBytes)
}

// HandshakeIdleTimeout fails a handshake that makes no progress for this
// long. Progress-based instead of absolute so slow-but-alive links (high
// latency is the norm for tunneled handshakes) always complete while a peer
// that stops sending entirely surfaces an error to callers. Zero disables.
const HandshakeIdleTimeout = 15 * time.Second

// handshakeMaxTimeout caps a handshake whose peer never goes silent, e.g.
// one that dribbles a byte at a time.
const handshakeMaxTimeout = 2 * time.Minute

// HandshakeTimeoutError reports a handshake that stopped making progress.
type HandshakeTimeoutError struct{ idle time.Duration }

func (e *HandshakeTimeoutError) Error() string {
	return fmt.Sprintf("ssh handshake made no progress for %s", e.idle)
}

func (e *HandshakeTimeoutError) Timeout() bool { return true }

// Temporary reports the error as transient so callers with net.Error retry
// handling treat it as worth retrying.
func (e *HandshakeTimeoutError) Temporary() bool { return true }

// ClientFromConn creates an SSH client over an existing network connection.
// The handshake is bounded by HandshakeIdleTimeout so a stalled peer surfaces
// an error instead of blocking the tunnel forever. No deadlines are set on
// the conn, so the established session is unaffected.
func ClientFromConn(conn net.Conn, user string, keyBytes []byte) (*ssh.Client, error) {
	return clientFromConn(conn, user, keyBytes, HandshakeIdleTimeout)
}

// activityConn records the time of the last successful read or write. The
// stored time keeps its monotonic reading so idle measurement is immune to
// wall-clock corrections.
type activityConn struct {
	net.Conn
	tracking     atomic.Bool
	lastActivity atomic.Pointer[time.Time]
}

func (c *activityConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.recordActivity()
	}
	return n, err
}

func (c *activityConn) Write(b []byte) (int, error) {
	n, err := c.Conn.Write(b)
	if n > 0 {
		c.recordActivity()
	}
	return n, err
}

func (c *activityConn) recordActivity() {
	if !c.tracking.Load() {
		return
	}
	now := time.Now()
	c.lastActivity.Store(&now)
}

type handshakeResult struct {
	c     ssh.Conn
	chans <-chan ssh.NewChannel
	reqs  <-chan *ssh.Request
	err   error
}

func clientFromConn(
	conn net.Conn,
	user string,
	keyBytes []byte,
	handshakeIdleTimeout time.Duration,
) (*ssh.Client, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection is required")
	}
	clientConfig, err := ConfigFromKeyBytes(keyBytes)
	if err != nil {
		return nil, err
	}

	clientConfig.User = user
	if handshakeIdleTimeout > 0 {
		return handshakeWithIdleTimeout(conn, clientConfig, handshakeIdleTimeout)
	}
	c, chans, req, err := ssh.NewClientConn(conn, "stdio", clientConfig)
	if err != nil {
		return nil, err
	}
	return ssh.NewClient(c, chans, handleKeepAliveRequests(req)), nil
}

// handshakeWithIdleTimeout runs the handshake in a goroutine and closes the
// conn when it stops making progress, so stall detection works on conns that
// do not support deadlines.
func handshakeWithIdleTimeout(
	conn net.Conn,
	clientConfig *ssh.ClientConfig,
	idle time.Duration,
) (*ssh.Client, error) {
	tracked := &activityConn{Conn: conn}
	tracked.tracking.Store(true)
	tracked.recordActivity()
	result := make(chan handshakeResult, 1)
	go func() {
		c, chans, req, err := ssh.NewClientConn(tracked, "stdio", clientConfig)
		result <- handshakeResult{c: c, chans: chans, reqs: req, err: err}
	}()

	ticker := time.NewTicker(idle / 4)
	defer ticker.Stop()
	deadline := time.After(handshakeMaxTimeout)
	for {
		select {
		case res := <-result:
			tracked.tracking.Store(false)
			if res.err != nil {
				return nil, res.err
			}
			return ssh.NewClient(res.c, res.chans, handleKeepAliveRequests(res.reqs)), nil
		case <-ticker.C:
			if time.Since(*tracked.lastActivity.Load()) > idle {
				tracked.tracking.Store(false)
				_ = conn.Close()
				return nil, &HandshakeTimeoutError{idle: idle}
			}
		case <-deadline:
			tracked.tracking.Store(false)
			_ = conn.Close()
			return nil, &HandshakeTimeoutError{idle: handshakeMaxTimeout}
		}
	}
}

func ConfigFromKeyBytes(keyBytes []byte) (*ssh.ClientConfig, error) {
	clientConfig := &ssh.ClientConfig{
		Auth:            []ssh.AuthMethod{},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	// key file authentication?
	if len(keyBytes) > 0 {
		signer, err := ssh.ParsePrivateKey(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}

		clientConfig.Auth = append(clientConfig.Auth, ssh.PublicKeys(signer))
	}
	return clientConfig, nil
}

type RunOptions struct {
	Client  *ssh.Client
	Command string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	EnvVars map[string]string
}

type RunSessionOptions struct {
	Command string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	EnvVars map[string]string
}

// ExitError wraps an SSH exit error with the exit code.
type ExitError struct {
	ExitCode int
	Err      error
}

func (e *ExitError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("exit status %d: %v", e.ExitCode, e.Err)
	}
	return fmt.Sprintf("exit status %d", e.ExitCode)
}

func (e *ExitError) Unwrap() error {
	return e.Err
}

func (opts *RunOptions) validate() error {
	if opts.Client == nil {
		return fmt.Errorf("SSH client is required")
	}
	if opts.Command == "" {
		return fmt.Errorf("command is required")
	}
	return nil
}

func Run(ctx context.Context, opts RunOptions) error {
	if err := opts.validate(); err != nil {
		return err
	}

	sess, err := opts.Client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer func() { _ = sess.Close() }()
	return RunSession(ctx, sess, RunSessionOptions{
		Command: opts.Command,
		Stdin:   opts.Stdin,
		Stdout:  opts.Stdout,
		Stderr:  opts.Stderr,
		EnvVars: opts.EnvVars,
	})
}

// RunSession executes a command on a caller-owned SSH session. It never closes
// the session; callers retain ownership and are responsible for cleanup.
func RunSession(ctx context.Context, sess *ssh.Session, opts RunSessionOptions) error {
	if sess == nil {
		return fmt.Errorf("SSH session is required")
	}
	if opts.Command == "" {
		return fmt.Errorf("command is required")
	}
	// Set environment variables (best effort - SSH servers may reject env vars or not support them)
	for k, v := range opts.EnvVars {
		_ = sess.Setenv(k, v) // Ignore errors - command should work without env vars
	}

	cleanup, err := setupContextCancellation(ctx, sess)
	if err != nil {
		return err
	}
	defer cleanup()

	sess.Stdin = opts.Stdin
	sess.Stdout = opts.Stdout
	sess.Stderr = opts.Stderr

	err = sess.Run(opts.Command)
	if err != nil {
		return handleRunError(ctx, err, opts.Command)
	}

	return nil
}

func setupContextCancellation(ctx context.Context, sess *ssh.Session) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context already cancelled: %w", err)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = sess.Signal(ssh.SIGINT)
		case <-done:
		}
	}()
	return func() { close(done) }, nil
}

func handleRunError(ctx context.Context, err error, command string) error {
	// If the context was cancelled, EOF and other errors are expected
	// from the session tearing down.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Check for exit errors with exit codes
	var exitErr *ssh.ExitError
	if errors.As(err, &exitErr) {
		exitCode := exitErr.ExitStatus()

		if isSignalInterrupt(exitCode) {
			return nil
		}

		// Return exit code for all other cases
		return &ExitError{
			ExitCode: exitCode,
			Err:      exitErr,
		}
	}

	// Provide context for common errors
	if errors.Is(err, io.EOF) {
		return fmt.Errorf("SSH session closed unexpectedly while running %s: %w", command, err)
	}

	return fmt.Errorf("SSH command failed while running %s: %w", command, err)
}

// isSignalInterrupt reports whether an exit code corresponds to a process
// terminated by a signal that ends an interactive session normally.
// Exit codes follow the 128+N convention: 130 = SIGINT, 129 = SIGHUP,
// 143 = SIGTERM.
func isSignalInterrupt(exitCode int) bool {
	switch exitCode {
	case 130, 129, 143:
		return true
	default:
		return false
	}
}
