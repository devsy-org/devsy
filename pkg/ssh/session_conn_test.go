package ssh

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/transport"
	"github.com/stretchr/testify/require"
	xssh "golang.org/x/crypto/ssh"
)

const (
	sessionTestCommand = "cat"
	sessionTestUser    = "session-user"
)

func TestOpenSessionConnRequiresInputs(t *testing.T) {
	if _, err := OpenSessionConn(
		context.Background(),
		nil,
		SessionConnOptions{Command: sessionTestCommand},
	); err == nil {
		t.Fatal("nil client should fail")
	}
}

func TestSessionConnCloseDuringWrite(t *testing.T) {
	client := newSessionTestClient(t)
	for range 20 {
		conn, err := OpenSessionConn(
			context.Background(),
			client,
			SessionConnOptions{Command: sessionTestCommand},
		)
		require.NoError(t, err)
		var wg sync.WaitGroup
		wg.Go(func() {
			for {
				if _, err := conn.Write([]byte("data")); err != nil {
					return
				}
			}
		})
		closeWithin(t, conn)
		wg.Wait()
		_, err = conn.Write([]byte("data"))
		require.ErrorIs(t, err, net.ErrClosed)
	}
}

func TestSessionConnCancelDuringManagedHandshake(t *testing.T) {
	client := newSessionTestClient(t)
	for range 20 {
		ctx, cancel := context.WithCancel(context.Background())
		conn, err := OpenSessionConn(ctx, client, SessionConnOptions{Command: sessionTestCommand})
		require.NoError(t, err)
		writing := &writeSignalConn{ManagedConn: conn, started: make(chan struct{})}
		result := make(chan error, 1)
		go func() {
			_, err := ClientFromManagedConn(
				ctx,
				writing,
				ManagedClientOptions{User: sessionTestUser},
			)
			result <- err
		}()
		waitForSignal(t, writing.started, "managed handshake did not write its identification")
		cancel()
		err = receiveTestError(t, result, "managed handshake did not stop after cancellation")
		require.ErrorIs(t, err, context.Canceled)
		closeWithin(t, conn)
	}
}

// writeSignalConn reports when the managed handshake starts writing.
type writeSignalConn struct {
	transport.ManagedConn
	once    sync.Once
	started chan struct{}
}

func (c *writeSignalConn) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.started) })
	return c.ManagedConn.Write(p)
}

func closeWithin(t *testing.T, conn io.Closer) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		_ = conn.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("session conn close hung")
	}
}

// newSessionTestClient serves sessions whose command never writes output, so
// a managed SSH handshake over them never receives a server identification.
func newSessionTestClient(t *testing.T) *xssh.Client {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	conn := openManagedTestConn(t, func(ctx context.Context, peer net.Conn) error {
		return serveTestSessions(ctx, peer, testServerConfig(t))
	})
	t.Cleanup(cancel)
	client, err := ClientFromManagedConn(ctx, conn, ManagedClientOptions{User: sessionTestUser})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func serveTestSessions(ctx context.Context, peer net.Conn, config *xssh.ServerConfig) error {
	serverConn, channels, requests, err := xssh.NewServerConn(peer, config)
	if err != nil {
		return err
	}
	go xssh.DiscardRequests(requests)
	go func() {
		for newChannel := range channels {
			channel, channelRequests, err := newChannel.Accept()
			if err != nil {
				continue
			}
			go func() {
				for req := range channelRequests {
					_ = req.Reply(req.Type == "exec", nil)
				}
			}()
			go func() {
				_, _ = io.Copy(io.Discard, channel)
				_ = channel.Close()
			}()
		}
	}()
	<-ctx.Done()
	_ = serverConn.Close()
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil
	}
	return ctx.Err()
}
