package snapshot

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	devcontainerconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

const (
	testDefaultContext = "default"
	testBindMountType  = "bind"
	testNormalEnvVar   = "NORMAL_VAR"
	testNormalEnvValue = "value"
	testLeakedValue    = "leaked"
)

func TestResolveRegistry_FlagWins(t *testing.T) {
	devsyConfig := &config.Config{
		Contexts:       map[string]*config.ContextConfig{testDefaultContext: {}},
		DefaultContext: testDefaultContext,
	}
	got, err := resolveRegistry("ghcr.io/acme/flag-repo", devsyConfig)
	require.NoError(t, err)
	require.Equal(t, "ghcr.io/acme/flag-repo", got)
}

func TestResolveRegistry_FallsBackToContextOption(t *testing.T) {
	devsyConfig := &config.Config{
		Contexts: map[string]*config.ContextConfig{
			testDefaultContext: {Options: map[string]config.OptionValue{
				config.ContextOptionSnapshotRegistry: {Value: "ghcr.io/acme/ctx-repo"},
			}},
		},
		DefaultContext: testDefaultContext,
	}
	got, err := resolveRegistry("", devsyConfig)
	require.NoError(t, err)
	require.Equal(t, "ghcr.io/acme/ctx-repo", got)
}

func TestCheckLocalWorkspace_RejectsMachineProvider(t *testing.T) {
	workspaceConfig := &provider.Workspace{
		ID:      "my-workspace",
		Machine: provider.WorkspaceMachineConfig{ID: "machine-abc"},
	}

	err := checkLocalWorkspace(workspaceConfig)
	require.Error(t, err)
	require.Contains(t, err.Error(), "my-workspace")
	require.Contains(t, err.Error(), "machine provider")
}

func TestCheckLocalWorkspace_AllowsLocalWorkspace(t *testing.T) {
	workspaceConfig := &provider.Workspace{ID: "my-workspace"}

	require.NoError(t, checkLocalWorkspace(workspaceConfig))
}

func TestCheckSingleMount_RejectsMultipleMounts(t *testing.T) {
	mounts := []*devcontainerconfig.Mount{
		{Type: testBindMountType, Source: "/host/a", Target: "/workspaces/a"},
		{Type: testBindMountType, Source: "/host/b", Target: "/workspaces/b"},
	}

	err := checkSingleMount(mounts)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not yet support multiple mounts")
}

func TestCheckSingleMount_AllowsSingleMount(t *testing.T) {
	mounts := []*devcontainerconfig.Mount{
		{Type: testBindMountType, Source: "/host/a", Target: "/workspaces/a"},
	}

	require.NoError(t, checkSingleMount(mounts))
}

func TestCheckSingleMount_RejectsNoMounts(t *testing.T) {
	err := checkSingleMount(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires a workspace mount")
}

func TestRedactedContainerEnv_DropsWorkspaceDaemonConfig(t *testing.T) {
	env := map[string]string{
		testNormalEnvVar:                testNormalEnvValue,
		config.EnvWorkspaceDaemonConfig: "base64-encoded-platform-access-key-payload",
	}

	got := redactedContainerEnv(env)

	require.Equal(t, map[string]string{testNormalEnvVar: testNormalEnvValue}, got)
}

func TestRedactedContainerEnv_LeavesUnaffectedEnvUntouched(t *testing.T) {
	env := map[string]string{testNormalEnvVar: testNormalEnvValue}

	got := redactedContainerEnv(env)

	require.Equal(t, env, got)
}

func TestRedactedContainerEnv_NilInputStaysNil(t *testing.T) {
	require.Nil(t, redactedContainerEnv(nil))
}

func TestRedactedContainerEnv_DropsCredentialLikeKeys(t *testing.T) {
	env := map[string]string{
		testNormalEnvVar:    testNormalEnvValue,
		"API_TOKEN":         testLeakedValue,
		"my_secret":         testLeakedValue,
		"DB_PASSWORD":       testLeakedValue,
		"apiKey":            testLeakedValue,
		"SSH_PRIVATE_KEY":   testLeakedValue,
		"AWS_ACCESS_KEY_ID": testLeakedValue,
		"GITHUB_PAT":        testLeakedValue,
		"CREDENTIAL":        testLeakedValue,
		"AUTHORIZATION":     testLeakedValue,
	}

	got := redactedContainerEnv(env)

	require.Equal(t, map[string]string{testNormalEnvVar: testNormalEnvValue}, got)
}

func TestRedactedContainerEnv_DoesNotDropUnrelatedKeys(t *testing.T) {
	env := map[string]string{
		testNormalEnvVar: testNormalEnvValue,
		"PATH":           "/usr/bin",
		"UPDATE_CHANNEL": "stable",
	}

	got := redactedContainerEnv(env)

	require.Equal(t, env, got)
}

func TestResolveRegistry_ErrorsWhenUnset(t *testing.T) {
	devsyConfig := &config.Config{
		Contexts:       map[string]*config.ContextConfig{testDefaultContext: {}},
		DefaultContext: testDefaultContext,
	}
	_, err := resolveRegistry("", devsyConfig)
	require.Error(t, err)
	require.Contains(t, err.Error(), "SNAPSHOT_REGISTRY")
}
