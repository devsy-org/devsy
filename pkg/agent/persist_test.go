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
		Origin:     dir,
		Workspace:  &provider2.Workspace{ID: "ws-test"},
		CLIOptions: provider2.CLIOptions{DaemonInterval: "3s"},
	}

	require.NoError(t, PersistAgentWorkspaceInfo(info))

	got, err := ParseAgentWorkspaceInfo(filepath.Join(dir, provider2.WorkspaceConfigFile))
	require.NoError(t, err)
	assert.Equal(t, "ws-test", got.Workspace.ID)
	assert.Empty(t, got.CLIOptions.DaemonInterval, "CLIOptions must not be persisted")
	assert.Equal(t, "3s", info.CLIOptions.DaemonInterval, "caller's struct must not be mutated")
}
