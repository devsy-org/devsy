package workspace

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os/user"
	"testing"
	"time"

	sshserver "github.com/devsy-org/devsy/pkg/ssh/server"
	"github.com/stretchr/testify/require"
	gossh "golang.org/x/crypto/ssh"
)

func TestCheckKeepAliveResponse(t *testing.T) {
	t.Parallel()

	transportErr := errors.New("connection closed")
	tests := []struct {
		name string
		ok   bool
		err  error
		want error
	}{
		{name: "positive reply", ok: true},
		{name: "negative reply proves peer liveness", ok: false},
		{
			name: "positive reply with transport error",
			ok:   true,
			err:  transportErr,
			want: transportErr,
		},
		{
			name: "negative reply with transport error",
			ok:   false,
			err:  transportErr,
			want: transportErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkKeepAliveResponse(tt.ok, tt.err)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("checkKeepAliveResponse() = %v, want nil", got)
				}
				return
			}
			if got == nil || !errors.Is(got, tt.want) {
				t.Fatalf("checkKeepAliveResponse() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckKeepAliveResponseWithDevsySSHServer(t *testing.T) {
	t.Parallel()

	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	hostKeyBlock, err := gossh.MarshalPrivateKey(hostKey, "test host key")
	require.NoError(t, err)
	hostKeyPEM := pem.EncodeToMemory(hostKeyBlock)
	hostSigner, err := gossh.NewSignerFromKey(hostKey)
	require.NoError(t, err)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	server, err := sshserver.NewServer(
		listener.Addr().String(),
		hostKeyPEM,
		nil,
		t.TempDir(),
		"",
	)
	require.NoError(t, err)

	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()

	currentUser, err := user.Current()
	require.NoError(t, err)
	client, err := gossh.Dial("tcp", listener.Addr().String(), &gossh.ClientConfig{
		User:            currentUser.Username,
		HostKeyCallback: gossh.FixedHostKey(hostSigner.PublicKey()),
		Timeout:         2 * time.Second,
	})
	require.NoError(t, err)
	defer func() {
		_ = client.Close()
		_ = server.Shutdown(context.Background())
		select {
		case <-serveDone:
		case <-time.After(2 * time.Second):
			t.Error("SSH server did not shut down")
		}
	}()

	ok, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
	require.NoError(t, err)
	require.False(t, ok, "the production server should reject its unsupported keepalive request")
	require.NoError(t, checkKeepAliveResponse(ok, err))

	session, err := client.NewSession()
	require.NoError(t, err)
	require.NoError(t, session.Run("true"))
	_ = session.Close()
}
