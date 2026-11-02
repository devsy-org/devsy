package tunnel

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"io"
	"net"
	"testing"
	"time"

	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func TestOptionalGitHubServiceFallbackAcrossSSH(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		fallback      bool
	}{
		{"legacy-agent", "unknown flag: --host-owner", true},
		{"runtime-failure", "credential transport stopped", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := failingServicesSSH(t, `{"kind":"error","message":"`+tc.message+`"}`)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			opts := RunServicesOptions{
				ContainerClient:            client,
				User:                       "vscode",
				HostOwner:                  "fixture-owner",
				ConfigureGitHubCredentials: true,
				ConfigureGitCredentials:    true,
			}
			calls := 0
			err := runOptionalGitHubServices(ctx, opts, func(current RunServicesOptions) error {
				calls++
				if calls == 1 {
					return runServicesIteration(ctx, current, nil, nil)
				}
				require.True(t, tc.fallback)
				require.False(t, current.ConfigureGitHubCredentials)
				require.True(t, current.ConfigureGitCredentials)
				return nil
			})
			if tc.fallback {
				require.NoError(t, err)
				require.Equal(t, 2, calls)
			} else {
				var exit *devssh.ExitError
				require.ErrorAs(t, err, &exit)
				require.Equal(t, 1, calls)
			}
		})
	}
}

func failingServicesSSH(t *testing.T, diagnostic string) *ssh.Client {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(key)
	require.NoError(t, err)
	serverConfig := &ssh.ServerConfig{NoClientAuth: true}
	serverConfig.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		serveServicesFailure(conn, serverConfig, diagnostic)
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), 5*time.Second)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client, err := devssh.ClientFromConn(conn, "vscode", nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func serveServicesFailure(conn net.Conn, serverConfig *ssh.ServerConfig, diagnostic string) {
	defer func() { _ = conn.Close() }()
	_, channels, requests, err := ssh.NewServerConn(conn, serverConfig)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(requests)
	for incoming := range channels {
		channel, requests, err := incoming.Accept()
		if err != nil {
			return
		}
		for request := range requests {
			if request.Type != "exec" {
				_ = request.Reply(false, nil)
				continue
			}
			_ = request.Reply(true, nil)
			_, _ = io.WriteString(channel.Stderr(), diagnostic+"\n")
			_, _ = channel.SendRequest(
				"exit-status",
				false,
				ssh.Marshal(struct{ Status uint32 }{1}),
			)
			_ = channel.Close()
			break
		}
	}
}
