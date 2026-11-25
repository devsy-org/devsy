package ssh

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/transport"
	"github.com/stretchr/testify/require"
	xssh "golang.org/x/crypto/ssh"
)

const (
	execServerTimeout = 10 * time.Second
	handshakeCancels  = 25
	innerCommand      = "inner"
	chunkSize         = 32 * 1024
	windowSize        = 2 * 1024 * 1024
)

// startExecServer serves one SSH connection whose session channels accept an
// exec request and then call handle with the channel. It returns a client.
func startExecServer(t *testing.T, handle func(ch xssh.Channel)) *xssh.Client {
	t.Helper()
	signer := newTestSSHSigner(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	config := &xssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		serverConn, chans, reqs, handshakeErr := xssh.NewServerConn(conn, config)
		if handshakeErr != nil {
			return
		}
		defer func() { _ = serverConn.Close() }()
		go xssh.DiscardRequests(reqs)
		var wg sync.WaitGroup
		defer wg.Wait()
		for newChannel := range chans {
			wg.Go(func() { serveExecChannel(newChannel, handle) })
		}
	}()
	client, err := xssh.Dial("tcp", listener.Addr().String(), &xssh.ClientConfig{
		User:            "exec-test",
		HostKeyCallback: xssh.FixedHostKey(signer.PublicKey()),
		Timeout:         2 * time.Second,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = client.Close()
		_ = listener.Close()
		<-serverDone
	})
	return client
}

func serveExecChannel(newChannel xssh.NewChannel, handle func(ch xssh.Channel)) {
	if newChannel.ChannelType() != "session" {
		_ = newChannel.Reject(xssh.UnknownChannelType, "sessions only")
		return
	}
	channel, requests, err := newChannel.Accept()
	if err != nil {
		return
	}
	defer func() { _ = channel.Close() }()
	for req := range requests {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		_ = req.Reply(true, nil)
		go func() {
			for range requests {
			}
		}()
		handle(channel)
		return
	}
}

// writeSignalConn reports when the handshake goroutine enters Write, so the
// test cancels only once that Write is under way.
type writeSignalConn struct {
	transport.ManagedConn
	entered chan struct{}
	once    sync.Once
}

func (c *writeSignalConn) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.entered) })
	return c.ManagedConn.Write(p)
}

// Cancelling the dial while the handshake goroutine writes its SSH
// identification makes Close send EOF on the same channel. Run under -race:
// before the fix the two touched channel.sentEOF with no ordering between them.
// The peer never reads, so nothing the peer does orders the two accesses.
func TestSessionConnCancelDuringHandshakeIsRaceFree(t *testing.T) {
	release := make(chan struct{})
	client := startExecServer(t, func(xssh.Channel) { <-release })
	t.Cleanup(func() { close(release) })

	for range handshakeCancels {
		ctx, cancel := context.WithCancel(t.Context())
		opened, err := OpenSessionConn(ctx, client, SessionConnOptions{Command: innerCommand})
		require.NoError(t, err)
		conn := &writeSignalConn{ManagedConn: opened, entered: make(chan struct{})}
		dialDone := make(chan error, 1)
		go func() {
			_, dialErr := ClientFromManagedConn(ctx, conn, ManagedClientOptions{})
			dialDone <- dialErr
		}()

		waitForTestSignal(t, conn.entered, "handshake never wrote")
		cancel()
		select {
		case err := <-dialDone:
			require.Error(t, err)
		case <-time.After(execServerTimeout):
			t.Fatal("handshake cancel did not return")
		}
	}
}

// fillWindow writes until the peer's flow-control window is full and one more
// Write is blocked. The returned channel reports that blocked writer's result.
func fillWindow(t *testing.T, conn net.Conn) <-chan error {
	t.Helper()
	var written atomic.Int64
	writeDone := make(chan error, 1)
	go func() {
		buf := make([]byte, chunkSize)
		for range 2 * windowSize / chunkSize {
			if _, writeErr := conn.Write(buf); writeErr != nil {
				writeDone <- writeErr
				return
			}
			written.Add(chunkSize)
		}
		writeDone <- nil
	}()
	require.Eventually(t, func() bool { return written.Load() >= windowSize },
		execServerTimeout, time.Millisecond, "writer never filled the window")
	return writeDone
}

// A Write blocked on the peer's flow-control window must not stop Close: Close
// returns, and the blocked Write fails once the channel is closed.
func TestSessionConnCloseUnblocksStalledWrite(t *testing.T) {
	release := make(chan struct{})
	client := startExecServer(t, func(xssh.Channel) { <-release })
	t.Cleanup(func() { close(release) })
	conn, err := OpenSessionConn(t.Context(), client, SessionConnOptions{Command: innerCommand})
	require.NoError(t, err)

	writeDone := fillWindow(t, conn)
	closeDone := make(chan error, 1)
	go func() { closeDone <- conn.Close() }()
	select {
	case <-closeDone:
	case <-time.After(execServerTimeout):
		t.Fatal("Close blocked behind a stalled Write")
	}
	select {
	case err := <-writeDone:
		require.Error(t, err)
	case <-time.After(execServerTimeout):
		t.Fatal("stalled Write did not fail after Close")
	}
}

// A Write that starts after Close returns net.ErrClosed without waiting for
// writeMu. The test holds the lock itself, standing in for a Write stalled on
// the peer's window, so a Write that queued on it would never return.
func TestSessionConnWriteAfterCloseDoesNotWaitForWriteLock(t *testing.T) {
	release := make(chan struct{})
	client := startExecServer(t, func(xssh.Channel) { <-release })
	t.Cleanup(func() { close(release) })
	opened, err := OpenSessionConn(t.Context(), client, SessionConnOptions{Command: innerCommand})
	require.NoError(t, err)
	conn, ok := opened.(*sessionConn)
	require.True(t, ok)

	conn.writeMu.Lock()
	defer conn.writeMu.Unlock()
	require.NoError(t, conn.Close())
	lateDone := make(chan error, 1)
	go func() {
		_, lateErr := conn.Write([]byte("late"))
		lateDone <- lateErr
	}()
	select {
	case err := <-lateDone:
		require.ErrorIs(t, err, net.ErrClosed)
	case <-time.After(execServerTimeout):
		t.Fatal("Write after Close waited for the write lock")
	}
}

func waitForTestSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(execServerTimeout):
		t.Fatal(failure)
	}
}
