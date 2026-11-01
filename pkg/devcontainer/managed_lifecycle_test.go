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
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	managedWorkspaceHost   = "workspace"
	managedWorkspaceFolder = "/workspaces/app"
	testEntrypoint         = "/app"
)

type managedMockDriver struct {
	*mockDriver
	shutdownAction string
}

func (*managedMockDriver) ManagedLifecycle() bool { return true }

func (d *managedMockDriver) SetLifecycleShutdownAction(action string) {
	d.shutdownAction = action
}

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
			ContainerWorkspaceFolder: managedWorkspaceFolder,
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

	require.NoError(
		t,
		r.injectDaemonEntrypoint(resolveParamsFor(devsy.PlatformOptions{}), merged, false, false),
	)

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

	require.NoError(t, r.injectDaemonEntrypoint(resolveParamsFor(platform), merged, false, false))

	cfg := injectedConfig(t, merged)
	assert.Equal(t, "key", cfg.Platform.AccessKey)
	assert.Equal(t, "root", cfg.Ssh.User)
	assert.Equal(t, "1m0s", cfg.Timeout)
}

func TestInjectDaemonEntrypointUnmanagedWithoutPlatformIsUntouched(t *testing.T) {
	r := newTestRunner(&mockDriver{})
	r.workspaceConfig.Workspace = &provider.Workspace{}
	merged := mergedWithEnv()

	require.NoError(
		t,
		r.injectDaemonEntrypoint(resolveParamsFor(devsy.PlatformOptions{}), merged, false, false),
	)

	assert.Empty(t, merged.ContainerEnv)
}

func TestInjectDaemonEntrypointResolvesLegacyAlias(t *testing.T) {
	r, _ := managedRunner("")
	r.workspaceConfig.Agent.Kubernetes.InactivityTimeout = "5m"
	merged := mergedWithEnv()

	require.NoError(
		t,
		r.injectDaemonEntrypoint(resolveParamsFor(devsy.PlatformOptions{}), merged, false, false),
	)

	assert.Equal(t, "5m0s", injectedConfig(t, merged).Timeout)
}

func TestInjectDaemonEntrypointPropagatesConfigErrors(t *testing.T) {
	r, _ := managedRunner("1m")
	r.workspaceConfig.Agent.Kubernetes.InactivityTimeout = "2m"

	err := r.injectDaemonEntrypoint(
		resolveParamsFor(devsy.PlatformOptions{}),
		mergedWithEnv(),
		false,
		false,
	)

	assert.ErrorContains(t, err, "conflicts")
}

func TestInjectDaemonEntrypointShutdownNoneDisablesTimeout(t *testing.T) {
	r, _ := managedRunner("1m")
	merged := mergedWithEnv()
	merged.ShutdownAction = config.ShutdownActionNone

	require.NoError(
		t,
		r.injectDaemonEntrypoint(resolveParamsFor(devsy.PlatformOptions{}), merged, false, false),
	)

	cfg := injectedConfig(t, merged)
	assert.Empty(t, cfg.Timeout)
	assert.Equal(t, config.ShutdownActionNone, cfg.ShutdownAction)
}

func TestWorkspaceInfoCarriesEffectiveTimeoutAndOwner(t *testing.T) {
	r, _ := managedRunner("")
	r.workspaceConfig.Agent.Kubernetes.InactivityTimeout = "90s"

	compressed, err := r.compressWorkspaceConfig(mergedWithEnv(), false, false)
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

func TestDockerlessBootstrapDoesNotRequireManagedOwner(t *testing.T) {
	for _, platform := range []devsy.PlatformOptions{
		{},
		{AccessKey: "key", PlatformHost: "platform", WorkspaceHost: managedWorkspaceHost},
	} {
		name := "without platform"
		if platform.AccessKey != "" {
			name = "with platform"
		}
		t.Run(name, func(t *testing.T) {
			r, _ := managedRunner("")
			merged := mergedWithEnv()
			params := resolveParamsFor(platform)
			require.NoError(t, r.injectDaemonEntrypoint(params, merged, true, false))

			info, err := r.prepareSetupInfo(&setupContainerParams{
				rawConfig: &config.DevContainerConfig{},
				containerDetails: &config.ContainerDetails{
					Dockerless: true,
					State:      config.ContainerDetailsState{Status: config.ContainerStatusRunning},
				},
				mergedConfig:        merged,
				substitutionContext: params.substitutionContext,
			})
			require.NoError(t, err)
			workspace, _, err := pkgagent.DecodeContainerWorkspaceInfo(
				info.workspaceConfigCompressed,
			)
			require.NoError(t, err)
			assert.Empty(t, workspace.LifecycleOwner)
			assert.Empty(t, workspace.ContainerTimeout)

			daemon := injectedConfig(t, merged)
			assert.Equal(t, merged.ShutdownAction, daemon.ShutdownAction)
			assert.False(t, daemon.Lifecycle.Managed())
			assert.False(t, daemon.Lifecycle.SetupArmsTimer)
			assert.Empty(t, daemon.Timeout)
			if platform.AccessKey == "" {
				assert.Empty(t, daemon.Platform.AccessKey)
				assert.Empty(t, daemon.Ssh)
				return
			}
			assert.Equal(t, platform, daemon.Platform)
			assert.Equal(
				t,
				agentd.SshConfig{User: containerRootUser, Workdir: managedWorkspaceFolder},
				daemon.Ssh,
			)
		})
	}
}

func TestDockerlessProducerRejectsEnabledTimeout(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "canonical"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			r, _ := managedRunner("1m")
			if legacy {
				r.workspaceConfig.Agent.ContainerTimeout = ""
				r.workspaceConfig.Agent.Kubernetes.InactivityTimeout = "1m"
			}
			merged := mergedWithEnv()
			err := r.injectDaemonEntrypoint(
				resolveParamsFor(devsy.PlatformOptions{}),
				merged,
				true,
				false,
			)
			require.ErrorContains(t, err, "dockerless")
			assert.NotContains(t, merged.ContainerEnv, pkgconfig.EnvWorkspaceDaemonConfig)

			_, err = r.compressWorkspaceConfig(merged, true, false)
			assert.ErrorContains(t, err, "dockerless")
		})
	}
}

func TestDockerlessProducerHonorsDisabledTimeout(t *testing.T) {
	for _, disableDaemon := range []bool{false, true} {
		name := "shutdown none"
		if disableDaemon {
			name = "disabled daemon"
		}
		t.Run(name, func(t *testing.T) {
			r, _ := managedRunner("1m")
			merged := mergedWithEnv()
			if disableDaemon {
				r.workspaceConfig.CLIOptions.DisableDaemon = true
			} else {
				merged.ShutdownAction = config.ShutdownActionNone
			}
			require.NoError(
				t,
				r.injectDaemonEntrypoint(
					resolveParamsFor(devsy.PlatformOptions{}),
					merged,
					true,
					false,
				),
			)
			compressed, err := r.compressWorkspaceConfig(merged, true, false)
			require.NoError(t, err)
			workspace, _, err := pkgagent.DecodeContainerWorkspaceInfo(compressed)
			require.NoError(t, err)
			assert.Empty(t, workspace.LifecycleOwner)
			assert.Empty(t, workspace.ContainerTimeout)
			daemon := injectedConfig(t, merged)
			assert.Equal(t, merged.ShutdownAction, daemon.ShutdownAction)
			assert.False(t, daemon.Lifecycle.Managed())
			assert.Empty(t, daemon.Timeout)
		})
	}
}

func TestZeroTimeoutKubernetesStillRequiresManagedOwner(t *testing.T) {
	r, _ := managedRunner("")
	merged := mergedWithEnv()
	require.NoError(
		t,
		r.injectDaemonEntrypoint(resolveParamsFor(devsy.PlatformOptions{}), merged, false, false),
	)
	assert.True(t, injectedConfig(t, merged).Lifecycle.Managed())

	compressed, err := r.compressWorkspaceConfig(merged, false, false)
	require.NoError(t, err)
	workspace, _, err := pkgagent.DecodeContainerWorkspaceInfo(compressed)
	require.NoError(t, err)
	assert.Equal(t, provider.ContainerLifecycleOwnerManagedDaemon, workspace.LifecycleOwner)
	assert.Empty(t, workspace.ContainerTimeout)
}

func TestZeroTimeoutOwnershipFollowsActualImageMain(t *testing.T) {
	no := false
	for _, tt := range []struct {
		name     string
		override *bool
		image    config.ImageDetailsConfig
		managed  bool
	}{
		{
			name:    "default override retains the daemon",
			image:   config.ImageDetailsConfig{Entrypoint: []string{testEntrypoint}},
			managed: true,
		},
		{name: "empty image main retains the daemon", override: &no, managed: true},
		{
			name: "external entrypoint owns the container", override: &no,
			image: config.ImageDetailsConfig{Entrypoint: []string{testEntrypoint}},
		},
		{
			name: "external command owns the container", override: &no,
			image: config.ImageDetailsConfig{Cmd: []string{testEntrypoint}},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := managedRunner("")
			merged := mergedWithEnv()
			merged.OverrideCommand = tt.override
			image := &config.ImageDetails{Config: tt.image}
			external := usesImageEntrypoint(merged, image)
			require.NoError(t, r.injectDaemonEntrypoint(
				resolveParamsFor(devsy.PlatformOptions{}), merged, false, external,
			))
			assert.Equal(t, tt.managed, injectedConfig(t, merged).Lifecycle.Managed())

			_, args := GetContainerEntrypointAndArgs(merged, image)
			if external {
				want := append([]string{}, tt.image.Entrypoint...)
				want = append(want, tt.image.Cmd...)
				assert.Equal(t, want, args[3:])
			} else {
				assert.Empty(t, args[3:])
			}
			compressed, err := r.compressWorkspaceConfig(merged, false, external)
			require.NoError(t, err)
			info, _, err := pkgagent.DecodeContainerWorkspaceInfo(compressed)
			require.NoError(t, err)
			if tt.managed {
				assert.Equal(t, provider.ContainerLifecycleOwnerManagedDaemon, info.LifecycleOwner)
			} else {
				assert.Empty(t, info.LifecycleOwner)
			}
		})
	}
}

func TestUnsupportedManagedRecreatePreservesExistingContainer(t *testing.T) {
	const (
		workspaceUID     = "workspace-uid"
		inspectedImageID = "image"
		builtImageName   = "built-image"
		baseImageName    = "alpine"
	)
	r, d := managedRunner("1m")
	r.workspaceConfig.Workspace = &provider.Workspace{ID: r.id, UID: workspaceUID}
	image := &config.ImageDetails{ID: inspectedImageID, Config: config.ImageDetailsConfig{
		Entrypoint: []string{testEntrypoint},
	}}
	r.imageBackend = &separateImages{
		details: image,
		buildInfo: &config.BuildInfo{
			ImageName: builtImageName, ImageDetails: image,
			ImageMetadata: &config.ImageMetadataConfig{},
		},
	}
	r.reporter = status.Nop()
	no := false
	params := recreateResolveParams()
	params.options.Recreate = true
	params.parsedConfig.Config.Image = baseImageName
	params.parsedConfig.Config.OverrideCommand = &no
	params.parsedConfig.Raw = config.CloneDevContainerConfig(params.parsedConfig.Config)
	params.substitutionContext = &config.SubstitutionContext{}

	_, _, err := r.buildNewContainerConfig(context.Background(), params)

	require.Error(t, err)
	assert.False(t, d.stopCalled)
	assert.False(t, d.deleteCalled)
}
