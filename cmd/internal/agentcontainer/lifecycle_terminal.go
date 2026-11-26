//go:build !windows

package agentcontainer

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/devcontainer/setup"
	"github.com/devsy-org/devsy/pkg/provider"
)

func (cmd *SetupContainerCmd) preAttach(
	ctx context.Context,
	state *containerState,
	cfg *setup.ContainerSetupConfig,
	open func(context.Context, tunnel.TunnelClient, provider.LifecycleTerminalRequest) (tunnel.TerminalSession, error),
) (setup.DeferredHooks, error) {
	if cmd.protocolSet {
		session, err := open(
			ctx,
			state.tunnelClient,
			*state.workspaceInfo.CLIOptions.LifecycleTerminal,
		)
		if err != nil {
			return setup.DeferredHooks{}, err
		}
		defer func() { _ = session.Close(ctx) }()
		if err := setup.ValidateInteractivePostStart(
			state.setupInfo.MergedConfig.PostStartCommands,
		); err != nil {
			return setup.DeferredHooks{}, err
		}
		cfg.Terminal = session
	}
	return setup.SetupContainerPreAttach(ctx, cfg)
}

func setupSignalContext(
	ctx context.Context,
	interactive bool,
) (context.Context, context.CancelFunc) {
	if !interactive {
		return ctx, func() {}
	}
	return signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
}
