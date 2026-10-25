package devcontainer

import (
	"context"
	"testing"
	"time"

	"github.com/devsy-org/api/pkg/devsy"
	pkgagent "github.com/devsy-org/devsy/pkg/agent"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	agentd "github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testEntrypoint = "/app"

type managedMockDriver struct{ *mockDriver }

func (*managedMockDriver) ManagedLifecycle() bool { return true }

func managedRunner(timeout string) (*runner, *managedMockDriver) {
	d := &managedMockDriver{mockDriver: &mockDriver{}}
	r := newTestRunner(d)
	r.workspaceConfig.Workspace = &provider.Workspace{}
	r.workspaceConfig.Agent.ContainerTimeout = timeout
	return r, d
}

func resolveParamsFor(platform devsy.PlatformOptions) *resolveParams {
	return &resolveParams{
		substitutionContext: &config.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/app",
		},
		options: UpOptions{CLIOptions: provider.CLIOptions{Platform: platform}},
	}
}

func mergedWithEnv() *config.MergedDevContainerConfig {
	merged := &config.MergedDevContainerConfig{}
	merged.ContainerEnv = map[string]string{}
	merged.ShutdownAction = config.ShutdownActionStopContainer
	return merged
}

func injectedConfig(t *testing.T, merged *config.MergedDevContainerConfig) *agentd.DaemonConfig {
	t.Helper()
	encoded, ok := merged.ContainerEnv[pkgconfig.EnvWorkspaceDaemonConfig]
	require.True(t, ok, "daemon config was not injected")
	cfg, err := agentd.DecodeDaemonConfig(encoded)
	require.NoError(t, err)
	return cfg
}

func TestInjectDaemonEntrypointManagedWithoutPlatform(t *testing.T) {
	r, _ := managedRunner("1m")
	merged := mergedWithEnv()

	require.NoError(t, r.injectDaemonEntrypoint(resolveParamsFor(devsy.PlatformOptions{}), merged))

	cfg := injectedConfig(t, merged)
	assert.Equal(t, "1m0s", cfg.Timeout)
	assert.Equal(t, config.ShutdownActionStopContainer, cfg.ShutdownAction)
	assert.True(t, cfg.Lifecycle.Managed())
	assert.Empty(t, cfg.Ssh, "no embedded SSH for a timeout-only config")
	assert.Empty(t, cfg.Platform.AccessKey)
}

func TestInjectDaemonEntrypointManagedWithPlatformKeepsNetworking(t *testing.T) {
	r, _ := managedRunner("1m")
	merged := mergedWithEnv()
	platform := devsy.PlatformOptions{AccessKey: "key", PlatformHost: "p", WorkspaceHost: "w"}

	require.NoError(t, r.injectDaemonEntrypoint(resolveParamsFor(platform), merged))

	cfg := injectedConfig(t, merged)
	assert.Equal(t, "key", cfg.Platform.AccessKey)
	assert.Equal(t, "root", cfg.Ssh.User)
	assert.Equal(t, "1m0s", cfg.Timeout)
}

func TestInjectDaemonEntrypointUnmanagedWithoutPlatformIsUntouched(t *testing.T) {
	r := newTestRunner(&mockDriver{})
	r.workspaceConfig.Workspace = &provider.Workspace{}
	merged := mergedWithEnv()

	require.NoError(t, r.injectDaemonEntrypoint(resolveParamsFor(devsy.PlatformOptions{}), merged))

	assert.Empty(t, merged.ContainerEnv)
}

func TestInjectDaemonEntrypointResolvesLegacyAlias(t *testing.T) {
	r, _ := managedRunner("")
	r.workspaceConfig.Agent.Kubernetes.InactivityTimeout = "5m"
	merged := mergedWithEnv()

	require.NoError(t, r.injectDaemonEntrypoint(resolveParamsFor(devsy.PlatformOptions{}), merged))

	assert.Equal(t, "5m0s", injectedConfig(t, merged).Timeout)
}

func TestInjectDaemonEntrypointPropagatesConfigErrors(t *testing.T) {
	r, _ := managedRunner("1m")
	r.workspaceConfig.Agent.Kubernetes.InactivityTimeout = "2m"

	err := r.injectDaemonEntrypoint(resolveParamsFor(devsy.PlatformOptions{}), mergedWithEnv())

	assert.ErrorContains(t, err, "conflicts")
}

func TestInjectDaemonEntrypointShutdownNoneDisablesTimeout(t *testing.T) {
	r, _ := managedRunner("1m")
	merged := mergedWithEnv()
	merged.ShutdownAction = config.ShutdownActionNone

	require.NoError(t, r.injectDaemonEntrypoint(resolveParamsFor(devsy.PlatformOptions{}), merged))

	cfg := injectedConfig(t, merged)
	assert.Empty(t, cfg.Timeout)
	assert.Equal(t, config.ShutdownActionNone, cfg.ShutdownAction)
}

func TestWorkspaceInfoCarriesEffectiveTimeoutAndOwner(t *testing.T) {
	r, _ := managedRunner("")
	r.workspaceConfig.Agent.Kubernetes.InactivityTimeout = "90s"

	compressed, err := r.compressWorkspaceConfig(mergedWithEnv())
	require.NoError(t, err)
	info, _, err := pkgagent.DecodeContainerWorkspaceInfo(compressed)
	require.NoError(t, err)

	assert.Equal(t, "1m30s", info.ContainerTimeout)
	assert.Equal(t, provider.ContainerLifecycleOwnerManagedDaemon, info.LifecycleOwner)
}

func TestManagedTimeoutConflict(t *testing.T) {
	no, yes := false, true
	withEntrypoint := &config.BuildInfo{ImageDetails: &config.ImageDetails{
		Config: config.ImageDetailsConfig{Entrypoint: []string{testEntrypoint}},
	}}
	empty := &config.BuildInfo{ImageDetails: &config.ImageDetails{}}

	tests := []struct {
		name     string
		override *bool
		build    *config.BuildInfo
		wantErr  string
	}{
		{name: "default override", build: withEntrypoint},
		{name: "explicit override", override: &yes, build: withEntrypoint},
		{
			name:     "user entrypoint bypasses daemon",
			override: &no,
			build:    withEntrypoint,
			wantErr:  "overrideCommand",
		},
		{name: "no user entrypoint to bypass", override: &no, build: empty},
		{
			name:    "dockerless nested container",
			build:   &config.BuildInfo{Dockerless: &config.BuildInfoDockerless{}},
			wantErr: "dockerless",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged := &config.MergedDevContainerConfig{}
			merged.OverrideCommand = tt.override

			err := managedTimeoutConflict(time.Minute, merged, tt.build)

			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestFindRunningContainerOrFailRejectsTerminalObjectsForManagedDrivers(t *testing.T) {
	r, d := managedRunner("1m")
	d.findResult = &config.ContainerDetails{
		State: config.ContainerDetailsState{Status: config.ContainerStatusExited},
	}

	_, err := r.findRunningContainerOrFail(context.Background(), "start")

	assert.ErrorContains(t, err, "not running")

	plain := newTestRunner(&mockDriver{findResult: d.findResult})
	details, err := plain.findRunningContainerOrFail(context.Background(), "start")
	require.NoError(t, err)
	assert.NotNil(t, details, "other drivers keep their existing behavior")
}
