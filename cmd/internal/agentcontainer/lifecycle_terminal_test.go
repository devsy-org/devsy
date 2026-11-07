//go:build !windows

package agentcontainer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/setup"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type AgentContainerLifecycleTerminalSuite struct {
	suite.Suite
}

func TestAgentContainerLifecycleTerminalSuite(t *testing.T) {
	suite.Run(t, new(AgentContainerLifecycleTerminalSuite))
}

func (s *AgentContainerLifecycleTerminalSuite) TestLifecycleTerminalGuard() {
	t := s.T()
	require.NotNil(
		t,
		NewSetupContainerCmd(&flags.GlobalFlags{}).Flags().Lookup("lifecycle-terminal-protocol"),
	)
	for _, tc := range []struct {
		set, present   bool
		version        int
		requestVersion uint32
		valid          bool
	}{
		{false, false, 0, 0, true},
		{true, true, 1, 1, true},
		{true, false, 1, 0, false},
		{false, true, 0, 1, false},
		{true, true, 0, 1, false},
		{true, true, 2, 1, false},
		{true, true, 1, 0, false},
		{true, true, 1, 2, false},
	} {
		cmd := &SetupContainerCmd{protocolSet: tc.set, LifecycleTerminalProtocol: tc.version}
		var request *provider.LifecycleTerminalRequest
		if tc.present {
			request = &provider.LifecycleTerminalRequest{ProtocolVersion: tc.requestVersion}
		}
		require.Equal(t, tc.valid, cmd.validateTerminal(request) == nil)
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

func (s *AgentContainerLifecycleTerminalSuite) TestPreAttachClosesSessionOnInvalidHooks() {
	t := s.T()
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

func (s *AgentContainerLifecycleTerminalSuite) TestSetupSignalContext() {
	t := s.T()
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

func (s *AgentContainerLifecycleTerminalSuite) TestRunRejectsRequestVersionBeforeInitialization() {
	t := s.T()
	info := provider.ContainerWorkspaceInfo{
		CLIOptions: provider.CLIOptions{
			LifecycleTerminal: &provider.LifecycleTerminalRequest{ProtocolVersion: 2},
		},
	}
	data, err := json.Marshal(info)
	require.NoError(t, err)
	encoded, err := compress.Compress(string(data))
	require.NoError(t, err)
	command := &SetupContainerCmd{
		ContainerWorkspaceInfo:    encoded,
		protocolSet:               true,
		LifecycleTerminalProtocol: 1,
	}
	setup, err := compress.Compress(`{}`)
	require.NoError(t, err)
	command.SetupInfo = setup
	require.ErrorContains(
		t,
		command.Run(context.Background()),
		"lifecycle terminal protocol and request mismatch",
	)
}
