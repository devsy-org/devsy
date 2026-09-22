package ssh

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestExitError_ErrorIncludesErrWhenSet(t *testing.T) {
	inner := errors.New("permission denied")
	err := &ExitError{ExitCode: 1, Err: inner}

	assert.Equal(t, "exit status 1: permission denied", err.Error())
}

func TestExitError_ErrorOmitsErrWhenNil(t *testing.T) {
	err := &ExitError{ExitCode: 127}

	assert.Equal(t, "exit status 127", err.Error())
}

func TestExitError_UnwrapReturnsWrappedErr(t *testing.T) {
	inner := errors.New("boom")
	err := &ExitError{ExitCode: 2, Err: inner}

	require.ErrorIs(t, err, inner)
	assert.Same(t, inner, errors.Unwrap(err))
}

func TestRunOptions_ValidateRequiresClient(t *testing.T) {
	err := (&RunOptions{Command: "ls"}).validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSH client is required")
}

func TestRunOptions_ValidateRequiresCommand(t *testing.T) {
	err := (&RunOptions{Client: &ssh.Client{}}).validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "command is required")
}

func TestRunOptions_ValidatePassesForClientAndCommand(t *testing.T) {
	err := (&RunOptions{Client: &ssh.Client{}, Command: "ls"}).validate()

	assert.NoError(t, err)
}

func TestIsSignalInterrupt(t *testing.T) {
	signalExits := []int{130, 129, 143}
	for _, code := range signalExits {
		assert.True(t, isSignalInterrupt(code), "exit code %d should be a signal interrupt", code)
	}

	nonSignalExits := []int{0, 1, 2, 127, 128, 131, 142, 144, 255, -1}
	for _, code := range nonSignalExits {
		assert.False(
			t,
			isSignalInterrupt(code),
			"exit code %d should not be a signal interrupt",
			code,
		)
	}
}

func TestHandleRunError_CancelledContextReturnsContextErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := handleRunError(ctx, io.EOF, "cmd")

	require.ErrorIs(t, err, context.Canceled)
}

func TestHandleRunError_EOFIsWrappedWithCommand(t *testing.T) {
	err := handleRunError(context.Background(), io.EOF, "build")

	require.ErrorIs(t, err, io.EOF)
	assert.Contains(t, err.Error(), "SSH session closed unexpectedly while running build")
}

func TestHandleRunError_GenericErrorIsWrappedWithCommand(t *testing.T) {
	inner := errors.New("connection reset")
	err := handleRunError(context.Background(), inner, "test")

	require.ErrorIs(t, err, inner)
	assert.Contains(t, err.Error(), "SSH command failed while running test")
}

func TestHandleRunError_ExitErrorIsWrappedInDevsyExitError(t *testing.T) {
	sshExitErr := &ssh.ExitError{}

	err := handleRunError(context.Background(), sshExitErr, "run")

	var devsyExit *ExitError
	require.ErrorAs(t, err, &devsyExit)
	assert.Equal(t, 0, devsyExit.ExitCode)
	require.ErrorIs(t, err, sshExitErr)
}

func TestSetupContextCancellation_AlreadyCancelledReturnsError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cleanup, err := setupContextCancellation(ctx, nil)

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, cleanup)
}

func TestSetupContextCancellation_ReturnsCleanupThatStopsWatcher(t *testing.T) {
	cleanup, err := setupContextCancellation(context.Background(), nil)

	require.NoError(t, err)
	require.NotNil(t, cleanup)

	assert.NotPanics(t, cleanup)
}

type deadlineRecorder struct {
	net.Conn
	deadlines []time.Time
}

func (d *deadlineRecorder) SetDeadline(t time.Time) error {
	d.deadlines = append(d.deadlines, t)
	return d.Conn.SetDeadline(t)
}

func TestClientFromConn_StalledHandshakeReturnsError(t *testing.T) {
	clientEnd, serverEnd := net.Pipe()
	defer func() { _ = serverEnd.Close() }()

	conn := &deadlineRecorder{Conn: clientEnd}
	start := time.Now()
	client, err := clientFromConn(conn, "", nil, 50*time.Millisecond)

	require.Error(t, err)
	assert.Nil(t, client)
	assert.Less(t, time.Since(start), 5*time.Second)
	var netErr net.Error
	require.ErrorAs(t, err, &netErr)
	assert.True(t, netErr.Timeout())
	assert.Empty(t, conn.deadlines, "handshake bound must not set conn deadlines")

	// the stalled conn is closed so the peer and any blocked goroutine unwind
	_, werr := clientEnd.Write([]byte("x"))
	require.Error(t, werr)
}

func TestClientFromConn_SuccessfulHandshake(t *testing.T) {
	// net.Pipe is unbuffered: both peers writing their version strings before
	// reading deadlocks, so the successful-handshake case needs a real socket.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(key)
	require.NoError(t, err)
	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(signer)
	go func() {
		serverEnd, err := listener.Accept()
		if err != nil {
			return
		}
		_, chans, reqs, err := ssh.NewServerConn(serverEnd, serverConfig)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		for ch := range chans {
			_ = ch.Reject(ssh.UnknownChannelType, "no channels in test")
		}
	}()

	clientEnd, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	conn := &deadlineRecorder{Conn: clientEnd}
	client, err := clientFromConn(conn, "test", nil, 5*time.Second)

	require.NoError(t, err)
	require.NotNil(t, client)
	defer func() { _ = client.Close() }()
	assert.Empty(t, conn.deadlines, "established session must stay free of deadlines")
}

// slowReadConn adds one-way latency to reads, simulating a high-latency link
// where the peer is alive but every message arrives late.
type slowReadConn struct {
	net.Conn
	delay time.Duration
}

func (c *slowReadConn) Read(b []byte) (int, error) {
	time.Sleep(c.delay)
	return c.Conn.Read(b)
}

func TestClientFromConn_SlowPeerStillCompletes(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(key)
	require.NoError(t, err)
	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(signer)
	go func() {
		serverEnd, err := listener.Accept()
		if err != nil {
			return
		}
		_, chans, reqs, err := ssh.NewServerConn(serverEnd, serverConfig)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		for ch := range chans {
			_ = ch.Reject(ssh.UnknownChannelType, "no channels in test")
		}
	}()

	clientEnd, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	conn := &slowReadConn{Conn: clientEnd, delay: 30 * time.Millisecond}

	client, err := clientFromConn(conn, "test", nil, 500*time.Millisecond)
	require.NoError(t, err, "slow-but-alive peer must not trip the idle timeout")
	require.NotNil(t, client)
	defer func() { _ = client.Close() }()
}
