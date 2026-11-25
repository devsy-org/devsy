package ssh

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	xssh "golang.org/x/crypto/ssh"
)

const (
	execServerTimeout = 10 * time.Second
	handshakeCancels  = 25
	cancelDelay       = 3 * time.Millisecond
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
		User:            "test",
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

// Cancelling the dial while the handshake goroutine writes its SSH
// identification makes Close send EOF on the same channel. Run under -race:
// before the fix the two touched channel.sentEOF with no ordering between them,
// whichever ran first.
func TestSessionConnCancelDuringHandshakeIsRaceFree(t *testing.T) {
	closed := make(chan struct{}, handshakeCancels)
	client := startExecServer(t, func(ch xssh.Channel) {
		_, _ = io.Copy(io.Discard, ch)
		closed <- struct{}{}
	})

	for range handshakeCancels {
		ctx, cancel := context.WithCancel(t.Context())
		conn, err := OpenSessionConn(ctx, client, SessionConnOptions{Command: "inner"})
		require.NoError(t, err)
		dialDone := make(chan error, 1)
		go func() {
			_, dialErr := ClientFromManagedConn(ctx, conn, ManagedClientOptions{})
			dialDone <- dialErr
		}()

		time.Sleep(cancelDelay)
		cancel()
		select {
		case err := <-dialDone:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(execServerTimeout):
			t.Fatal("handshake cancel did not return")
		}
		waitForTestSignal(t, closed, "peer never saw the session end")
	}
}

// A Write blocked on the peer's flow-control window must not stop Close: Close
// returns, and the blocked Write fails once the channel is closed.
func TestSessionConnCloseUnblocksStalledWrite(t *testing.T) {
	release := make(chan struct{})
	client := startExecServer(t, func(xssh.Channel) { <-release })
	t.Cleanup(func() { close(release) })
	conn, err := OpenSessionConn(t.Context(), client, SessionConnOptions{Command: "inner"})
	require.NoError(t, err)

	const chunk, window = 32 * 1024, 2 * 1024 * 1024
	var written atomic.Int64
	writeDone := make(chan error, 1)
	go func() {
		buf := make([]byte, chunk)
		for range 2 * window / chunk {
			if _, writeErr := conn.Write(buf); writeErr != nil {
				writeDone <- writeErr
				return
			}
			written.Add(chunk)
		}
		writeDone <- nil
	}()

	require.Eventually(t, func() bool { return written.Load() >= window },
		execServerTimeout, time.Millisecond, "writer never filled the window")
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

func waitForTestSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(execServerTimeout):
		t.Fatal(failure)
	}
}
