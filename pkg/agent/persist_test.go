package agent

import (
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPersistAgentWorkspaceInfo_RoundTripsResolvedConfig(t *testing.T) {
	dir := t.TempDir()
	info := &provider2.AgentWorkspaceInfo{
		Origin:    dir,
		Workspace: &provider2.Workspace{ID: "ws-test"},
		LastDevContainerConfig: &config.DevContainerConfigWithPath{
			Config: &config.DevContainerConfig{
				DevContainerConfigBase: config.DevContainerConfigBase{
					ShutdownAction: config.ShutdownActionNone,
				},
			},
		},
	}

	require.NoError(t, PersistAgentWorkspaceInfo(info))

	got, err := ParseAgentWorkspaceInfo(filepath.Join(dir, provider2.WorkspaceConfigFile))
	require.NoError(t, err)
	require.NotNil(t, got.LastDevContainerConfig)
	require.NotNil(t, got.LastDevContainerConfig.Config)
	assert.Equal(t, config.ShutdownActionNone, got.LastDevContainerConfig.Config.ShutdownAction)
}

func TestPersistAgentWorkspaceInfo_RequiresOrigin(t *testing.T) {
	assert.Error(t, PersistAgentWorkspaceInfo(&provider2.AgentWorkspaceInfo{}))
	assert.Error(t, PersistAgentWorkspaceInfo(nil))
}

func TestPersistAgentWorkspaceInfo_DoesNotPersistCLIOptions(t *testing.T) {
	dir := t.TempDir()
	info := &provider2.AgentWorkspaceInfo{
		Origin:    dir,
		Workspace: &provider2.Workspace{ID: "ws-test"},
		CLIOptions: provider2.CLIOptions{
			DaemonInterval: "3s",
			LifecycleTerminal: &provider2.LifecycleTerminalRequest{
				ProtocolVersion: 1,
				Nonce:           "ephemeral-nonce",
				Context:         "ctx",
				WorkspaceUID:    "uid",
			},
		},
	}

	require.NoError(t, PersistAgentWorkspaceInfo(info))

	got, err := ParseAgentWorkspaceInfo(filepath.Join(dir, provider2.WorkspaceConfigFile))
	require.NoError(t, err)
	assert.Equal(t, "ws-test", got.Workspace.ID)
	assert.Empty(t, got.CLIOptions.DaemonInterval, "CLIOptions must not be persisted")
	assert.Nil(
		t,
		got.CLIOptions.LifecycleTerminal,
		"lifecycle terminal requests must not be persisted",
	)
	assert.NotNil(
		t,
		info.CLIOptions.LifecycleTerminal,
		"cloning must not mutate the caller's options",
	)
	assert.Equal(t, "3s", info.CLIOptions.DaemonInterval, "caller's struct must not be mutated")
}
