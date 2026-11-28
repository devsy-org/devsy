package ssh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/stretchr/testify/require"
	cryptossh "golang.org/x/crypto/ssh"
)

type rejectedHandshakeConn struct{ cause error }

func (c rejectedHandshakeConn) Read([]byte) (int, error)         { return 0, c.cause }
func (c rejectedHandshakeConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c rejectedHandshakeConn) Close() error                     { return nil }
func (c rejectedHandshakeConn) LocalAddr() net.Addr              { return nil }
func (c rejectedHandshakeConn) RemoteAddr() net.Addr             { return nil }
func (c rejectedHandshakeConn) SetDeadline(time.Time) error      { return nil }
func (c rejectedHandshakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c rejectedHandshakeConn) SetWriteDeadline(time.Time) error { return nil }

func TestClientFromConnClassifiesTrustedNetworkError(t *testing.T) {
	cause := errors.New("connection canary")
	conn := rejectedHandshakeConn{cause: &net.OpError{Op: "read", Net: "tcp", Err: cause}}
	_, err := clientFromConn(conn, "user", nil, 0)
	require.ErrorIs(t, err, cause)
	var classified *clierr.CLIError
	require.ErrorAs(t, err, &classified)
	require.Equal(t, clierr.CodeSSHUnreachable, classified.Code)
}

func TestRemoteExit255DoesNotBecomeConnectionFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	_, key, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer, err := cryptossh.NewSignerFromKey(key)
	clientDone := make(chan struct{})
	defer close(clientDone)
	require.NoError(t, err)
	serverConfig := &cryptossh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(signer)
	serverDone := make(chan error, 1)
	go serveExit255(listener, serverConfig, serverDone, clientDone)
	client, err := NewSSHClient("user", listener.Addr().String(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	var stderr bytes.Buffer
	err = Run(context.Background(), RunOptions{Client: client, Command: "fake", Stderr: &stderr})
	var exit *ExitError
	require.ErrorAs(t, err, &exit)
	require.Equal(t, 255, exit.ExitCode)
	var classified *clierr.CLIError
	require.NotErrorAs(t, err, &classified)
	require.Equal(t, "ssh: connect to host forged: Connection refused\n", stderr.String())
	select {
	case err := <-serverDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("SSH fixture did not finish")
	}
}

func serveExit255(
	listener net.Listener,
	config *cryptossh.ServerConfig,
	done chan<- error,
	clientDone <-chan struct{},
) {
	conn, err := listener.Accept()
	if err != nil {
		done <- err
		return
	}
	defer func() { _ = conn.Close() }()
	server, channels, requests, err := cryptossh.NewServerConn(conn, config)
	if err != nil {
		done <- err
		return
	}
	defer func() { _ = server.Close() }()
	go cryptossh.DiscardRequests(requests)
	for incoming := range channels {
		channel, reqs, err := incoming.Accept()
		if err != nil {
			done <- err
			return
		}
		for req := range reqs {
			if req.Type != "exec" {
				_ = req.Reply(false, nil)
				continue
			}
			_ = req.Reply(true, nil)
			_, _ = io.WriteString(
				channel.Stderr(),
				"ssh: connect to host forged: Connection refused\n",
			)
			_, err = channel.SendRequest(
				"exit-status",
				false,
				cryptossh.Marshal(struct{ Status uint32 }{255}),
			)
			_ = channel.Close()
			done <- err
			<-clientDone
			return
		}
	}
	done <- nil
}
