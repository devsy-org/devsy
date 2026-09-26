package ssh

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	xssh "golang.org/x/crypto/ssh"
)

func TestClientFromManagedConnAllowsDelayedBootstrap(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	serverConfig := testServerConfig(t)
	conn := openManagedTestConn(t, func(ctx context.Context, peer net.Conn) error {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		return serveTestSSH(ctx, peer, serverConfig)
	})
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dialDone := make(chan struct {
		client *xssh.Client
		err    error
	}, 1)
	go func() {
		client, err := ClientFromManagedConn(ctx, conn, ManagedClientOptions{
			HandshakeIdleTimeout: 20 * time.Millisecond,
			HandshakeMaxTimeout:  time.Second,
		})
		dialDone <- struct {
			client *xssh.Client
			err    error
		}{client: client, err: err}
	}()

	<-started
	select {
	case result := <-dialDone:
		if result.client != nil {
			_ = result.client.Close()
		}
		t.Fatalf("managed dial ended during bootstrap: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)

	select {
	case result := <-dialDone:
		require.NoError(t, result.err)
		require.NotNil(t, result.client)
		_ = result.client.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("managed SSH handshake did not complete")
	}
}

func TestClientFromManagedConnReturnsBootstrapError(t *testing.T) {
	wantErr := errors.New("agent injection failed")
	conn := &resetOnStreamEndManagedConn{
		testManagedConn: openManagedTestConn(t, func(context.Context, net.Conn) error {
			return wantErr
		}),
	}
	defer func() { _ = conn.Close() }()

	_, err := ClientFromManagedConn(context.Background(), conn, ManagedClientOptions{
		HandshakeIdleTimeout: 20 * time.Millisecond,
	})

	require.ErrorIs(t, err, wantErr)
	var timeoutErr *HandshakeTimeoutError
	assert.NotErrorAs(t, err, &timeoutErr)
}

type resetOnStreamEndManagedConn struct{ *testManagedConn }

func (c *resetOnStreamEndManagedConn) Read(p []byte) (int, error) {
	n, err := c.testManagedConn.Read(p)
	if isManagedStreamEnd(err) {
		return n, &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}
	}
	return n, err
}

func TestClientFromManagedConnTimesOutAfterPeerStartsHandshake(t *testing.T) {
	conn := openManagedTestConn(t, func(ctx context.Context, peer net.Conn) error {
		if _, err := readSSHIdentification(peer); err != nil {
			return err
		}
		if _, err := io.WriteString(peer, "SSH-2.0-managed-test\r\n"); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	})
	defer func() { _ = conn.Close() }()

	_, err := ClientFromManagedConn(context.Background(), conn, ManagedClientOptions{
		HandshakeIdleTimeout: 40 * time.Millisecond,
		HandshakeMaxTimeout:  time.Second,
	})

	var timeoutErr *HandshakeTimeoutError
	require.ErrorAs(t, err, &timeoutErr)
	assert.Equal(t, 40*time.Millisecond, timeoutErr.idle)
}

func TestClientFromManagedConnEnforcesHandshakeMaximum(t *testing.T) {
	conn := openManagedTestConn(t, func(ctx context.Context, peer net.Conn) error {
		if _, err := readSSHIdentification(peer); err != nil {
			return err
		}
		if _, err := io.WriteString(peer, "SSH-2.0-managed-test\r\n"); err != nil {
			return err
		}
		<-ctx.Done()
		return ctx.Err()
	})
	defer func() { _ = conn.Close() }()

	_, err := ClientFromManagedConn(context.Background(), conn, ManagedClientOptions{
		HandshakeIdleTimeout: 500 * time.Millisecond,
		HandshakeMaxTimeout:  40 * time.Millisecond,
	})

	var timeoutErr *HandshakeTimeoutError
	require.ErrorAs(t, err, &timeoutErr)
	assert.Equal(t, 40*time.Millisecond, timeoutErr.idle)
}

func TestClientFromManagedConnCancellationClosesTransport(t *testing.T) {
	callbackDone := make(chan struct{})
	conn := openManagedTestConn(t, func(ctx context.Context, _ net.Conn) error {
		defer close(callbackDone)
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	dialDone := make(chan error, 1)
	go func() {
		_, err := ClientFromManagedConn(ctx, conn, ManagedClientOptions{})
		dialDone <- err
	}()

	cancel()
	select {
	case err := <-dialDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("managed SSH dial did not stop after cancellation")
	}
	select {
	case <-callbackDone:
	case <-time.After(time.Second):
		t.Fatal("managed transport callback was not terminated")
	}
	_ = conn.Close()
}

type testManagedConn struct {
	net.Conn
	peer   net.Conn
	cancel context.CancelFunc
	done   chan error
	once   sync.Once
}

func (c *testManagedConn) Wait() error { return <-c.done }

func (c *testManagedConn) Close() error {
	var err error
	c.once.Do(func() {
		c.cancel()
		err = c.Conn.Close()
		_ = c.peer.Close()
	})
	return err
}

func openManagedTestConn(
	t *testing.T,
	callback func(context.Context, net.Conn) error,
) *testManagedConn {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()
	clientConn, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	peer, err := listener.Accept()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	conn := &testManagedConn{
		Conn: clientConn, peer: peer, cancel: cancel, done: make(chan error, 1),
	}
	go func() {
		err := callback(ctx, peer)
		conn.done <- err
		_ = peer.Close()
	}()
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func testServerConfig(t *testing.T) *xssh.ServerConfig {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	signer, err := xssh.NewSignerFromKey(key)
	require.NoError(t, err)
	config := &xssh.ServerConfig{NoClientAuth: true}
	config.AddHostKey(signer)
	return config
}

func serveTestSSH(ctx context.Context, peer net.Conn, config *xssh.ServerConfig) error {
	serverConn, channels, requests, err := xssh.NewServerConn(peer, config)
	if err != nil {
		return err
	}
	go xssh.DiscardRequests(requests)
	go func() {
		for channel := range channels {
			_ = channel.Reject(xssh.UnknownChannelType, "test")
		}
	}()
	<-ctx.Done()
	_ = serverConn.Close()
	return ctx.Err()
}

func readSSHIdentification(conn net.Conn) (string, error) {
	return bufio.NewReader(conn).ReadString('\n')
}

func TestApplicationCodeUsesManagedSSHDialer(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	assertNoDirectSSHClientCall(t, filepath.Join(root, "cmd"), "")
	assertNoDirectSSHClientCall(t, filepath.Join(root, "pkg"), filepath.Join(root, "pkg", "ssh"))
}

func assertNoDirectSSHClientCall(t *testing.T, base, excludedDir string) {
	t.Helper()
	for _, path := range applicationGoFiles(t, base, excludedDir) {
		contents, err := os.ReadFile( // #nosec G304 -- path comes from the repository walk root.
			path,
		)
		require.NoError(t, err)
		if strings.Contains(string(contents), ".ClientFromConn(") {
			t.Errorf(
				"%s calls ClientFromConn outside pkg/ssh; classify readiness and use ClientFromManagedConn for managed transports",
				path,
			)
		}
	}
}

func applicationGoFiles(t *testing.T, base, excludedDir string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if excludedDir != "" && path == excludedDir {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	require.NoError(t, err)
	return paths
}
