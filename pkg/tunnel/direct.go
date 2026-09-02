package tunnel

import (
	"context"
	"io"

	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"github.com/devsy-org/devsy/pkg/transport"
)

// Tunnel defines the function to create an "outer" tunnel.
type Tunnel func(ctx context.Context, stdin io.Reader, stdout io.Writer) error

// NewTunnel creates a managed SSH tunnel using generic transport callbacks.
func NewTunnel(ctx context.Context, tunnel Tunnel, handler Handler) error {
	conn, err := transport.OpenCallbackConn(
		ctx,
		func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
			return tunnel(ctx, stdin, stdout)
		},
		transport.CallbackConnOptions{
			LocalAddr:  transport.NewAddr("tunnel"),
			RemoteAddr: transport.NewAddr("provider"),
		},
	)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	return transport.RunManaged(transport.RunManagedOptions{
		Parent:        ctx,
		Conn:          conn,
		TransportSide: transport.SideProvider,
		Metadata:      transport.LogMetadata{TransportImpl: "callback"},
		Handler: func(ctx context.Context) error {
			sshClient, err := devssh.ClientFromConn(conn, "", nil)
			if err != nil {
				return err
			}
			defer func() { _ = sshClient.Close() }()
			return handler(ctx, sshClient)
		},
	})
}
