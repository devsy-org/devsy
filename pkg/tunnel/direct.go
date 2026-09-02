package tunnel

import (
	"context"
	"io"

	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"github.com/devsy-org/devsy/pkg/transport"
)

// Tunnel defines the function to create an "outer" tunnel.
type Tunnel func(ctx context.Context, stdin io.Reader, stdout io.Writer) error

// NewTunnel creates a tunnel to the devcontainer using generic functions
// to establish the "outer" and "inner" tunnel, used by proxy clients.
// The tunnel will be an SSH connection with its STDIO as arguments and the
// handler will be the function to execute the command using the
// connected SSH client.
func NewTunnel(ctx context.Context, tunnel Tunnel, handler Handler) error {
	return newTunnel(ctx, tunnel, handler, TransportLogMetadata{})
}

// NewTunnelWithMetadata is NewTunnel with context for the canonical transport
// close event.
func NewTunnelWithMetadata(
	ctx context.Context,
	tunnel Tunnel,
	handler Handler,
	metadata TransportLogMetadata,
) error {
	return newTunnel(ctx, tunnel, handler, metadata)
}

func newTunnel(
	ctx context.Context,
	tunnel Tunnel,
	handler Handler,
	metadata TransportLogMetadata,
) error {
	conn, err := transport.OpenCallbackConn(ctx, func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
		return tunnel(ctx, stdin, stdout)
	}, transport.CallbackConnOptions{
		LocalAddr:  transport.NewAddr("tunnel"),
		RemoteAddr: transport.NewAddr("provider"),
	})
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	return transport.RunManaged(transport.RunManagedOptions{
		Parent:        ctx,
		Conn:          conn,
		TransportSide: transport.SideProvider,
		Metadata: transport.LogMetadata{
			Provider: metadata.Provider, Mode: metadata.Mode,
			Workspace: metadata.Workspace, TransportImpl: "callback",
		},
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
