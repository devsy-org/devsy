//go:build !windows

package agentcontainer

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/setup"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/require"
)

func TestLifecycleTerminalGuard(t *testing.T) {
	require.NotNil(
		t,
		NewSetupContainerCmd(&flags.GlobalFlags{}).Flags().Lookup("lifecycle-terminal-protocol"),
	)
	for _, tc := range []struct {
		set, present bool
		version      int
		valid        bool
	}{{false, false, 0, true}, {true, true, 1, true}, {true, false, 1, false}, {false, true, 0, false}, {true, true, 0, false}, {true, true, 2, false}} {
		cmd := &SetupContainerCmd{protocolSet: tc.set, LifecycleTerminalProtocol: tc.version}
		require.Equal(t, tc.valid, cmd.validateTerminal(tc.present) == nil)
	}
}

type testTerminalSession struct{ closed bool }

func (s *testTerminalSession) Begin(
	context.Context,
	tunnel.TerminalInvocation,
) (tunnel.TerminalAttachment, error) {
	return nil, errors.New("unexpected begin")
}
func (s *testTerminalSession) Close(context.Context) error { s.closed = true; return nil }

func TestPreAttachClosesSessionOnInvalidHooks(t *testing.T) {
	session := &testTerminalSession{}
	info := &config.Result{MergedConfig: &config.MergedDevContainerConfig{}}
	info.MergedConfig.PostStartCommands = []types.LifecycleHook{{"a": {"true"}, "b": {"true"}}}
	state := &containerState{
		setupInfo: info,
		workspaceInfo: &provider.ContainerWorkspaceInfo{
			CLIOptions: provider.CLIOptions{
				LifecycleTerminal: &provider.LifecycleTerminalRequest{},
			},
		},
	}
	cmd := &SetupContainerCmd{protocolSet: true}
	_, err := cmd.preAttach(
		context.Background(),
		state,
		&setup.ContainerSetupConfig{SetupInfo: info},
		func(context.Context, tunnel.TunnelClient, provider.LifecycleTerminalRequest) (tunnel.TerminalSession, error) {
			return session, nil
		},
	)
	require.ErrorContains(t, err, "postStartCommand")
	require.True(t, session.closed)
}

func TestSetupSignalContext(t *testing.T) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)
	ctx, stop := setupSignalContext(context.Background(), false)
	defer stop()
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))
	select {
	case <-signals:
	case <-time.After(5 * time.Second):
		t.Fatal("signal not received")
	}
	require.NoError(t, ctx.Err())
	interactive, cancel := setupSignalContext(context.Background(), true)
	defer cancel()
	require.NoError(t, syscall.Kill(os.Getpid(), syscall.SIGINT))
	select {
	case <-interactive.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("context not canceled")
	}
}
