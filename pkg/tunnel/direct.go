package tunnel

import (
	"context"
	"io"
	"os"

	devssh "github.com/devsy-org/devsy/pkg/ssh"
)

// Tunnel defines the function to create an "outer" tunnel.
type Tunnel func(ctx context.Context, stdin io.Reader, stdout io.Writer) error

// NewTunnel creates a tunnel to the devcontainer using generic functions
// to establish the "outer" and "inner" tunnel, used by proxy clients.
// The tunnel will be an SSH connection with its STDIO as arguments and
// the handler will be the function to execute the command using the
// connected SSH client.
func NewTunnel(ctx context.Context, tunnel Tunnel, handler Handler) error {
	pb, err := NewPipeBridge()
	if err != nil {
		return err
	}
	defer pb.Close()

	return pb.RunPair(ctx,
		func(ctx context.Context, stdin, stdout *os.File) error {
			return tunnel(ctx, stdin, stdout)
		},
		func(ctx context.Context, stdout, stdin *os.File) error {
			sshClient, err := devssh.StdioClient(stdout, stdin, false)
			if err != nil {
				return err
			}
			defer func() { _ = sshClient.Close() }()
			return handler(ctx, sshClient)
		},
	)
}
