package agentworkspace

import (
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
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
		set, present bool
		version      int
		valid        bool
	}{
		{false, false, 0, true},
		{true, true, 1, true},
		{true, false, 1, false},
		{false, true, 0, false},
		{true, true, 0, false},
		{true, true, 2, false},
	} {
		cmd := &UpCmd{protocolSet: tc.set, LifecycleTerminalProtocol: tc.version}
		require.Equal(t, tc.valid, cmd.validateTerminal(tc.present) == nil)
	}
}
