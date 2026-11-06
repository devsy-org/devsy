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
)

func TestValidateInteractivePostStartRejectsConflicts(t *testing.T) {
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
		t.Run(test.name, func(t *testing.T) {
			cmd := &UpCmd{InteractivePostStart: true}
			test.set(cmd)
			require.ErrorContains(t, cmd.validateInteractivePostStart(), "cannot be combined")
		})
	}
}

func TestInteractivePostStartFlagDefaultsFalse(t *testing.T) {
	root := NewUpCmd(&flags.GlobalFlags{})
	flag := root.Flags().Lookup(names.InteractivePostStart)
	require.NotNil(t, flag)
	require.Equal(t, "false", flag.DefValue)
	value, err := root.Flags().GetBool(names.InteractivePostStart)
	require.NoError(t, err)
	require.False(t, value)
}

func TestWorkspaceAgentCommandLifecycleGuard(t *testing.T) {
	base := workspaceAgentCommand("/agent", "workspace", false, false)
	require.NotContains(t, base, "lifecycle-terminal-protocol")
	interactive := workspaceAgentCommand("/agent", "workspace", false, true)
	require.Contains(t, interactive, shellescape.Quote("--lifecycle-terminal-protocol=1"))
	require.Equal(t, base+" "+shellescape.Quote("--lifecycle-terminal-protocol=1"), interactive)
}

func TestDefaultInvocationHasNoLifecycleTerminalRequest(t *testing.T) {
	cmd := &UpCmd{}
	require.False(t, cmd.InteractivePostStart)
	require.Nil(t, cmd.LifecycleTerminal)
}

func TestDispatchRejectsInteractiveUnsupportedRoutes(t *testing.T) {
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

func TestInteractivePostStartTerminalDimensions(t *testing.T) {
	for _, value := range []int{-1, 65536} {
		cmd := &UpCmd{CLIOptions: provider.CLIOptions{TerminalRows: value}}
		require.ErrorContains(t, cmd.validateInteractivePostStart(), "between 0 and 65535")
		cmd = &UpCmd{CLIOptions: provider.CLIOptions{TerminalColumns: value}}
		require.ErrorContains(t, cmd.validateInteractivePostStart(), "between 0 and 65535")
	}
	require.NoError(t, (&UpCmd{}).validateInteractivePostStart())
}

func TestInteractivePostStartRejectsJSONAndUI(t *testing.T) {
	cmd := &UpCmd{InteractivePostStart: true, GlobalFlags: &flags.GlobalFlags{ResultFormat: "json"}}
	require.ErrorContains(t, cmd.validateInteractivePostStart(), "requires plain output")
	t.Setenv("DEVSY_UI", "true")
	cmd = &UpCmd{InteractivePostStart: true, GlobalFlags: &flags.GlobalFlags{ResultFormat: "plain"}}
	require.ErrorContains(t, cmd.validateInteractivePostStart(), "DEVSY_UI")
}

func TestInteractivePostStartCIEnvironmentIsNotTerminalCheck(t *testing.T) {
	t.Setenv("CI", "true")
	cmd := &UpCmd{
		InteractivePostStart: true,
		GlobalFlags:          &flags.GlobalFlags{ResultFormat: "plain"},
	}
	err := cmd.validateInteractivePostStart()
	require.ErrorContains(t, err, "attached stdin and stdout")
	require.NotContains(t, err.Error(), "CI")
}

func TestCreateLifecycleTerminalRequest(t *testing.T) {
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
