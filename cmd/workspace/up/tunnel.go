package up

import (
	"context"
	"fmt"

	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/tunnel"
)

// startTunnel creates a local TCP tunnel that forwards connections to the
// container SSH server. The returned cleanup function must be called when
// the tunnel is no longer needed (typically via defer).
func (cmd *UpCmd) startTunnel(
	ctx context.Context,
	_ *config.Config,
	client client2.BaseWorkspaceClient,
	wctx *workspaceContext,
) (int, func(), error) {
	log.Info("Starting SSH tunnel for workspace")

	dialer := &tunnel.WorkspaceDialer{
		Context:   client.Context(),
		User:      wctx.user,
		Workspace: client.Workspace(),
		Workdir:   wctx.workdir,
		GPGAgent:  cmd.GPGAgentForwarding,
	}

	localTunnel, err := tunnel.NewLocalTunnel(ctx, tunnel.LocalTunnelOptions{
		BasePort: 10800,
		DialFunc: dialer.Dial,
	})
	if err != nil {
		return 0, nil, fmt.Errorf("create local tunnel: %w", err)
	}

	log.Infof("SSH tunnel listening on port %d", localTunnel.Port())

	cleanup := func() {
		_ = localTunnel.Close()
	}

	return localTunnel.Port(), cleanup, nil
}
