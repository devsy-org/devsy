package up

import (
	"context"
	"fmt"

	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/tunnel"
)

// workspaceTunnelHealth probes whether the workspace behind the tunnel is
// still running. It is strictly observational: it must never dial the SSH
// path or otherwise start the workspace, because a background health check
// that can revive a stopped workspace reverses deliberate stops.
func workspaceTunnelHealth(
	client client2.BaseWorkspaceClient,
) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		status, err := client.Status(ctx, client2.StatusOptions{ContainerStatus: true})
		if err != nil {
			return fmt.Errorf("workspace status: %w", err)
		}
		if status != client2.StatusRunning {
			return fmt.Errorf("workspace is %q", status)
		}
		return nil
	}
}

// startTunnel creates a local TCP tunnel that forwards connections to the
// container SSH server. The returned cleanup function must be called when
// the tunnel is no longer needed (typically via defer).
func (cmd *UpCmd) startTunnel(
	ctx context.Context,
	_ *config.Config,
	client client2.BaseWorkspaceClient,
	wctx *workspaceContext,
) (int, func(), error) {
	log.Debug("starting ssh tunnel for workspace")

	dialer := &tunnel.WorkspaceDialer{
		Context:   client.Context(),
		User:      wctx.user,
		Workspace: client.Workspace(),
		Workdir:   wctx.workdir,
		GPGAgent:  cmd.GPGAgentForwarding,
	}

	localTunnel, err := tunnel.NewLocalTunnel(ctx, tunnel.LocalTunnelOptions{
		BasePort:        10800,
		DialFunc:        dialer.Dial,
		HealthCheckFunc: workspaceTunnelHealth(client),
	})
	if err != nil {
		return 0, nil, fmt.Errorf("create local tunnel: %w", err)
	}

	log.Debugf("ssh tunnel listening on port %d", localTunnel.Port())

	cleanup := func() {
		_ = localTunnel.Close()
	}

	return localTunnel.Port(), cleanup, nil
}
