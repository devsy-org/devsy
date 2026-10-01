package devcontainer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestComposeSecretRuntimeOptions(t *testing.T) {
	workspace := &provider.AgentWorkspaceInfo{}
	workspace.CLIOptions.TerminalSecretEnvNames = []string{terminalSecretSentinelName}
	r := &runner{workspaceConfig: workspace, driver: terminalSecretMountDriver{supported: true}}
	options := UpOptions{}
	effective, refresh, err := r.composeSecretRuntimeOptions(nil, options)
	require.NoError(t, err)
	require.True(t, effective.Recreate)
	require.True(t, refresh)

	details := &config.ContainerDetails{
		State: config.ContainerDetailsState{Status: config.ContainerStatusRunning},
	}

	effective, refresh, err = r.composeSecretRuntimeOptions(details, options)
	require.NoError(t, err)
	require.True(t, effective.Recreate)
	require.True(t, refresh)

	details.Mounts = []config.ContainerMount{{
		Type: driver.MountTypeTmpfs, Destination: config.SecretsEnvDir,
	}}
	effective, refresh, err = r.composeSecretRuntimeOptions(details, options)
	require.NoError(t, err)
	require.False(t, effective.Recreate)
	require.False(t, refresh)

	r.driver = terminalSecretMountDriver{}
	details.Mounts = nil
	effective, refresh, err = r.composeSecretRuntimeOptions(details, options)
	require.Error(t, err)
	require.False(t, effective.Recreate)
	require.False(t, refresh)
}

func TestComposeFileSecretRuntimeOptions(t *testing.T) {
	r := &runner{driver: terminalSecretMountDriver{supported: true}}
	options := UpOptions{CLIOptions: provider.CLIOptions{
		SecretsMount: []string{secretFileMountRequestSentinel},
	}}
	effective, refresh, err := r.composeSecretRuntimeOptions(nil, options)
	require.NoError(t, err)
	require.True(t, effective.Recreate)
	require.True(t, refresh)

	details := &config.ContainerDetails{
		State: config.ContainerDetailsState{Status: config.ContainerStatusRunning},
	}

	effective, refresh, err = r.composeSecretRuntimeOptions(details, options)
	require.NoError(t, err)
	require.True(t, effective.Recreate)
	require.True(t, refresh)

	details.Mounts = []config.ContainerMount{{
		Type: driver.MountTypeTmpfs, Destination: config.SecretsMountDir,
	}}
	effective, refresh, err = r.composeSecretRuntimeOptions(details, options)
	require.NoError(t, err)
	require.False(t, effective.Recreate)
	require.False(t, refresh)

	r.driver = terminalSecretMountDriver{}
	details.Mounts = nil
	effective, refresh, err = r.composeSecretRuntimeOptions(details, options)
	require.Error(t, err)
	require.False(t, effective.Recreate)
	require.False(t, refresh)
}

func TestRestorePersistedComposeArgsForStartSkipsStaleOverrides(t *testing.T) {
	staleOverride := filepath.Join(t.TempDir(), FeaturesStartOverrideFilePrefix+"-stale.yml")
	require.NoError(t, os.WriteFile(staleOverride, []byte("stale"), 0o600))
	container := &config.ContainerDetails{Config: config.ContainerDetailsConfig{
		Labels: map[string]string{ConfigFilesLabel: staleOverride},
	}}
	baseArgs := []string{"-f", "compose.yaml"}

	args, restored := restorePersistedComposeArgsForStart(container, baseArgs, true)
	require.False(t, restored)
	require.Equal(t, baseArgs, args)

	args, restored = restorePersistedComposeArgsForStart(container, baseArgs, false)
	require.True(t, restored)
	require.Equal(t, append(baseArgs, "-f", staleOverride), args)
}
