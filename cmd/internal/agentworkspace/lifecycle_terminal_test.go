package agentworkspace

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type AgentWorkspaceLifecycleTerminalSuite struct {
	suite.Suite
}

func TestAgentWorkspaceLifecycleTerminalSuite(t *testing.T) {
	suite.Run(t, new(AgentWorkspaceLifecycleTerminalSuite))
}

func (s *AgentWorkspaceLifecycleTerminalSuite) TestLifecycleTerminalGuard() {
	t := s.T()
	require.NotNil(t, NewUpCmd(&flags.GlobalFlags{}).Flags().Lookup("lifecycle-terminal-protocol"))
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
		cmd := &UpCmd{protocolSet: tc.set, LifecycleTerminalProtocol: tc.version}
		var request *provider.LifecycleTerminalRequest
		if tc.present {
			request = &provider.LifecycleTerminalRequest{ProtocolVersion: tc.requestVersion}
		}
		require.Equal(t, tc.valid, cmd.validateTerminal(request) == nil)
	}
}

func (s *AgentWorkspaceLifecycleTerminalSuite) TestRunRejectsRequestVersionBeforeInitialization() {
	t := s.T()
	info := provider.AgentWorkspaceInfo{
		CLIOptions: provider.CLIOptions{
			LifecycleTerminal: &provider.LifecycleTerminalRequest{ProtocolVersion: 2},
		},
	}
	data, err := json.Marshal(info)
	require.NoError(t, err)
	encoded, err := compress.Compress(string(data))
	require.NoError(t, err)
	command := &UpCmd{WorkspaceInfo: encoded, protocolSet: true, LifecycleTerminalProtocol: 1}
	require.ErrorContains(
		t,
		command.Run(context.Background()),
		"lifecycle terminal protocol and request mismatch",
	)
}
