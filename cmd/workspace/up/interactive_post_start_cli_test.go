package up

import (
	"context"
	"testing"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/cmd/flags"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type InteractivePostStartCLISuite struct {
	suite.Suite
}

func TestInteractivePostStartCLISuite(t *testing.T) {
	suite.Run(t, new(InteractivePostStartCLISuite))
}

func (s *InteractivePostStartCLISuite) TestValidateInteractivePostStartRejectsConflicts() {
	tests := []struct {
		name string
		set  func(*UpCmd)
	}{
		{name: "detach", set: func(cmd *UpCmd) { cmd.Detach = true }},
		{name: "prebuild", set: func(cmd *UpCmd) { cmd.Prebuild = true }},
		{name: "recovery", set: func(cmd *UpCmd) { cmd.Recovery = true }},
		{name: "skip post start", set: func(cmd *UpCmd) { cmd.SkipPostStart = true }},
		{name: "skip nonblocking", set: func(cmd *UpCmd) { cmd.SkipNonBlockingCommands = true }},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			t := s.T()
			cmd := &UpCmd{InteractivePostStart: true}
			test.set(cmd)
			require.ErrorContains(t, cmd.validateInteractivePostStart(), "cannot be combined")
		})
	}
}

func (s *InteractivePostStartCLISuite) TestInteractivePostStartFlagDefaultsFalse() {
	t := s.T()
	root := NewUpCmd(&flags.GlobalFlags{})
	flag := root.Flags().Lookup(names.InteractivePostStart)
	require.NotNil(t, flag)
	require.Equal(t, "false", flag.DefValue)
	value, err := root.Flags().GetBool(names.InteractivePostStart)
	require.NoError(t, err)
	require.False(t, value)
}

func (s *InteractivePostStartCLISuite) TestWorkspaceAgentCommandLifecycleGuard() {
	t := s.T()
	base := workspaceAgentCommand("/agent", "workspace", false, false)
	require.NotContains(t, base, "lifecycle-terminal-protocol")
	interactive := workspaceAgentCommand("/agent", "workspace", false, true)
	require.Contains(t, interactive, shellescape.Quote("--lifecycle-terminal-protocol=1"))
	require.Equal(t, base+" "+shellescape.Quote("--lifecycle-terminal-protocol=1"), interactive)
}

func (s *InteractivePostStartCLISuite) TestDefaultInvocationHasNoLifecycleTerminalRequest() {
	t := s.T()
	cmd := &UpCmd{}
	require.False(t, cmd.InteractivePostStart)
	require.Nil(t, cmd.LifecycleTerminal)
}

func (s *InteractivePostStartCLISuite) TestDispatchRejectsInteractiveUnsupportedRoutes() {
	t := s.T()
	cmd := &UpCmd{InteractivePostStart: true}
	cmd.Platform.Enabled = true
	_, err := cmd.dispatchClient(context.Background(), &config.Config{}, nil)
	require.ErrorContains(t, err, "platform-managed")

	cmd.Platform.Enabled = false
	for _, client := range []client2.BaseWorkspaceClient{
		interactiveProxyClient{}, interactiveDaemonClient{},
	} {
		_, err := cmd.dispatchClient(context.Background(), &config.Config{}, client)
		require.ErrorContains(t, err, "unsupported")
	}
}

type (
	interactiveProxyClient  struct{ client2.ProxyClient }
	interactiveDaemonClient struct{ client2.DaemonClient }
)

func (s *InteractivePostStartCLISuite) TestInteractivePostStartTerminalDimensions() {
	t := s.T()
	for _, value := range []int{-1, 65536} {
		cmd := &UpCmd{CLIOptions: provider.CLIOptions{TerminalRows: value}}
		require.ErrorContains(t, cmd.validateInteractivePostStart(), "between 0 and 65535")
		cmd = &UpCmd{CLIOptions: provider.CLIOptions{TerminalColumns: value}}
		require.ErrorContains(t, cmd.validateInteractivePostStart(), "between 0 and 65535")
	}
	require.NoError(t, (&UpCmd{}).validateInteractivePostStart())
}

func (s *InteractivePostStartCLISuite) TestInteractivePostStartRejectsJSONAndUI() {
	t := s.T()
	cmd := &UpCmd{InteractivePostStart: true, GlobalFlags: &flags.GlobalFlags{ResultFormat: "json"}}
	require.ErrorContains(t, cmd.validateInteractivePostStart(), "requires plain output")
	t.Setenv("DEVSY_UI", "true")
	cmd = &UpCmd{InteractivePostStart: true, GlobalFlags: &flags.GlobalFlags{ResultFormat: "plain"}}
	require.ErrorContains(t, cmd.validateInteractivePostStart(), "DEVSY_UI")
}

func (s *InteractivePostStartCLISuite) TestInteractivePostStartCIEnvironmentIsNotTerminalCheck() {
	t := s.T()
	t.Setenv("CI", "true")
	cmd := &UpCmd{
		InteractivePostStart: true,
		GlobalFlags:          &flags.GlobalFlags{ResultFormat: "plain"},
	}
	err := cmd.validateInteractivePostStart()
	require.ErrorContains(t, err, "attached stdin and stdout")
	require.NotContains(t, err.Error(), "CI")
}

func (s *InteractivePostStartCLISuite) TestCreateLifecycleTerminalRequest() {
	t := s.T()
	cmd := &UpCmd{}
	client := &lifecycleTerminalRequestClient{}
	require.NoError(t, cmd.createLifecycleTerminalRequest(client))
	require.NotNil(t, cmd.LifecycleTerminal)
	require.Equal(t, uint32(1), cmd.LifecycleTerminal.ProtocolVersion)
	require.NotEmpty(t, cmd.LifecycleTerminal.Nonce)
	require.Equal(t, "context", cmd.LifecycleTerminal.Context)
	require.Equal(t, "workspace-uid", cmd.LifecycleTerminal.WorkspaceUID)
}

type lifecycleTerminalRequestClient struct {
	client2.BaseWorkspaceClient
}

func (*lifecycleTerminalRequestClient) Context() string { return "context" }

func (*lifecycleTerminalRequestClient) WorkspaceConfig() *provider.Workspace {
	return &provider.Workspace{UID: "workspace-uid"}
}
