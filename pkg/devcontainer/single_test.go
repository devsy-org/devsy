package devcontainer

import (
	"context"
	"errors"
	"strings"
	"testing"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/require"
)

const mountTypeVolume = "volume"

type provisioningPreflightMockDriver struct {
	*mockDriver
	provisioningCalled bool
	provisioningErr    error
}

func (d *provisioningPreflightMockDriver) ProvisioningPreflight(context.Context) error {
	d.provisioningCalled = true
	return d.provisioningErr
}

func TestResolveContainerRecreateProvisioningFailurePreservesExistingContainer(t *testing.T) {
	sentinel := errors.New("unsupported provisioning runtime")
	existing := runningContainerDetails()
	base := &mockDriver{findResult: existing}
	d := &provisioningPreflightMockDriver{
		mockDriver:      base,
		provisioningErr: sentinel,
	}
	r := newTestRunner(d)

	_, err := r.resolveContainer(
		context.Background(), recreateResolveParams(), existing,
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("resolveContainer error = %v, want %v", err, sentinel)
	}
	if !d.provisioningCalled {
		t.Fatal("expected provisioning preflight before recreate")
	}
	if base.stopCalled {
		t.Fatal("existing container was stopped before provisioning validation")
	}
	if base.deleteCalled {
		t.Fatal("existing container was deleted before provisioning validation")
	}
}

func TestResolveContainerExternalRecreateRejectsBeforeProvisioningPreflight(t *testing.T) {
	d := &provisioningPreflightMockDriver{mockDriver: &mockDriver{}}
	r := newTestRunner(d)
	params := recreateResolveParams()
	params.parsedConfig.Config.ContainerID = testContainerID

	_, err := r.resolveContainer(context.Background(), params, runningContainerDetails())
	if err == nil || err.Error() != "cannot recreate container not created by Devsy" {
		t.Fatalf("resolveContainer error = %v, want external-container recreate error", err)
	}
	if d.provisioningCalled {
		t.Fatal("provisioning preflight ran for an invalid external-container recreate")
	}
	if d.stopCalled || d.deleteCalled {
		t.Fatal("invalid external-container recreate changed container state")
	}
}

func TestStartComposeContainerRecreateProvisioningFailurePreservesExistingContainer(t *testing.T) {
	sentinel := errors.New("unsupported provisioning runtime")
	base := &mockDriver{}
	d := &provisioningPreflightMockDriver{
		mockDriver:      base,
		provisioningErr: sentinel,
	}
	r := newTestRunner(d)

	_, err := r.startContainer(context.Background(), &startContainerParams{
		container: runningContainerDetails(),
		options:   UpOptions{CLIOptions: provider.CLIOptions{Recreate: true}},
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("startContainer error = %v, want %v", err, sentinel)
	}
	if !d.provisioningCalled {
		t.Fatal("expected provisioning preflight before compose recreate")
	}
	if base.stopCalled {
		t.Fatal("existing container was stopped before provisioning validation")
	}
	if base.deleteCalled {
		t.Fatal("existing container was deleted before provisioning validation")
	}
}

func recreateResolveParams() *resolveParams {
	return &resolveParams{
		parsedConfig: &config.SubstitutedConfig{
			Config: &config.DevContainerConfig{},
		},
		options: UpOptions{CLIOptions: provider.CLIOptions{Recreate: true}},
	}
}

func runningContainerDetails() *config.ContainerDetails {
	return &config.ContainerDetails{
		ID:     testContainerID,
		State:  config.ContainerDetailsState{Status: testStatusRunning},
		Config: config.ContainerDetailsConfig{Labels: map[string]string{}},
	}
}

func TestWorkspaceMountDestination(t *testing.T) { //nolint:funlen // table-driven test
	tests := []struct {
		name   string
		mounts []config.ContainerMount
		want   string
	}{
		{
			name:   "no mounts",
			mounts: nil,
			want:   "",
		},
		{
			name: "bind mount under /workspaces/",
			mounts: []config.ContainerMount{
				{
					Type:        mountTypeBind,
					Source:      "/home/user/project",
					Destination: "/workspaces/my-app",
				},
			},
			want: "/workspaces/my-app",
		},
		{
			name: "volume mount under /workspaces/ is ignored",
			mounts: []config.ContainerMount{
				{
					Type:        mountTypeVolume,
					Source:      "myvol",
					Destination: "/workspaces/other",
				},
			},
			want: "",
		},
		{
			name: "bind mount outside /workspaces/ is ignored",
			mounts: []config.ContainerMount{
				{
					Type:        mountTypeBind,
					Source:      "/host/path",
					Destination: "/opt/data",
				},
			},
			want: "",
		},
		{
			name: "multiple mounts returns first workspace bind",
			mounts: []config.ContainerMount{
				{
					Type:        mountTypeVolume,
					Source:      "cache",
					Destination: "/cache",
				},
				{
					Type:        mountTypeBind,
					Source:      "/home/user/ws",
					Destination: "/workspaces/old-name",
				},
				{
					Type:        mountTypeBind,
					Source:      "/tmp/extra",
					Destination: "/extra",
				},
			},
			want: "/workspaces/old-name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			details := &config.ContainerDetails{
				ID:     testContainerID,
				State:  config.ContainerDetailsState{Status: testStatusRunning},
				Config: config.ContainerDetailsConfig{Labels: map[string]string{}},
				Mounts: tt.mounts,
			}

			got := workspaceMountDestination(details)
			if got != tt.want {
				t.Errorf("workspaceMountDestination() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSecretsEnvironmentTmpfsMount(t *testing.T) {
	mount, err := secretsEnvironmentTmpfsMount(true, true)
	if err != nil {
		t.Fatal("create secret environment mount")
	}
	if mount == nil || mount.Type != "tmpfs" || mount.Target != config.SecretsEnvDir {
		t.Error("secret environment mount is not configured for the protected directory")
	}
	if _, err := secretsEnvironmentTmpfsMount(true, false); err == nil {
		t.Error("unsupported tmpfs provider was allowed to store workspace secrets")
	}
	if mount, err := secretsEnvironmentTmpfsMount(false, false); err != nil || mount != nil {
		t.Error("secret-free workspace unexpectedly received a tmpfs mount")
	}
}

func TestWithSecretsMountUsesCurrentUpRequest(t *testing.T) {
	r := &runner{driver: terminalSecretMountDriver{supported: true}}
	mounts, err := r.withSecretsMount(nil, true)
	require.NoError(t, err)
	require.Len(t, mounts, 1)
	require.Equal(t, config.SecretsMountDir, mounts[0].Target)
	require.Equal(t, "tmpfs", mounts[0].Type)

	mounts, err = r.withSecretsMount(nil, false)
	require.NoError(t, err)
	require.Empty(t, mounts)
}

func TestWithResolvedUser(t *testing.T) {
	parsed := &config.DevContainerConfig{}
	parsed.RunArgs = []string{"--cap-add=SYS_PTRACE"}

	uid := true
	merged := &config.MergedDevContainerConfig{}
	merged.RemoteUser = "vscode"
	merged.ContainerUser = "node"
	merged.UpdateRemoteUserUID = &uid

	got := withResolvedUser(parsed, merged)

	if got.RemoteUser != "vscode" {
		t.Errorf("RemoteUser = %q, want vscode", got.RemoteUser)
	}
	if got.ContainerUser != "node" {
		t.Errorf("ContainerUser = %q, want node", got.ContainerUser)
	}
	if got.UpdateRemoteUserUID == nil || !*got.UpdateRemoteUserUID {
		t.Errorf("UpdateRemoteUserUID = %v, want true", got.UpdateRemoteUserUID)
	}
	if len(got.RunArgs) != 1 || got.RunArgs[0] != "--cap-add=SYS_PTRACE" {
		t.Errorf("RunArgs not preserved: %v", got.RunArgs)
	}
	if parsed.RemoteUser != "" {
		t.Error("source config must not be mutated")
	}
}

func TestRecoveryDevContainerConfig(t *testing.T) {
	source := &config.DevContainerConfig{}
	source.Image = "mcr.microsoft.com/devcontainers/go:1.26"
	source.Name = "my-project"
	source.Features = map[string]any{
		"ghcr.io/devcontainers-extra/features/go-task:1": map[string]any{},
	}
	source.OverrideFeatureInstallOrder = []string{"ghcr.io/devcontainers-extra/features/go-task"}
	source.PostCreateCommand = types.LifecycleHook{"install": {"npm install"}}
	source.OnCreateCommand = types.LifecycleHook{"setup": {"echo hi"}}

	parsed := &config.SubstitutedConfig{Config: source, Raw: source}

	got := recoveryDevContainerConfig(parsed)

	if len(got.Config.Features) != 0 {
		t.Errorf("Features must be cleared, got %v", got.Config.Features)
	}
	if len(got.Config.OverrideFeatureInstallOrder) != 0 {
		t.Errorf(
			"OverrideFeatureInstallOrder must be cleared, got %v",
			got.Config.OverrideFeatureInstallOrder,
		)
	}
	if len(got.Config.PostCreateCommand) != 0 || len(got.Config.OnCreateCommand) != 0 {
		t.Error("lifecycle hooks must be cleared")
	}
	if got.Config.Image != source.Image {
		t.Errorf("Image must be preserved, got %q", got.Config.Image)
	}
	if got.Config.Name != source.Name {
		t.Errorf("Name must be preserved, got %q", got.Config.Name)
	}
	if got.Raw != parsed.Raw {
		t.Error("Raw config must be preserved")
	}
}

func TestRecoveryDevContainerConfigNoMutation(t *testing.T) {
	source := &config.DevContainerConfig{}
	source.Features = map[string]any{"ghcr.io/x/y:1": map[string]any{}}
	source.PostCreateCommand = types.LifecycleHook{"install": {"npm install"}}

	recoveryDevContainerConfig(&config.SubstitutedConfig{Config: source, Raw: source})

	if len(source.Features) == 0 {
		t.Error("source Features must not be mutated")
	}
	if len(source.PostCreateCommand) == 0 {
		t.Error("source lifecycle hooks must not be mutated")
	}
}

func TestRecoveryDevContainerConfigDockerfile(t *testing.T) {
	source := &config.DevContainerConfig{}
	source.Dockerfile = "Dockerfile"
	source.Context = "."
	source.Features = map[string]any{"ghcr.io/x/y:1": map[string]any{}}

	parsed := &config.SubstitutedConfig{Config: source, Raw: source}

	got := recoveryDevContainerConfig(parsed)

	if got.Config.Image != defaultRecoveryImage {
		t.Errorf(
			"Image = %q, want default recovery image %q",
			got.Config.Image,
			defaultRecoveryImage,
		)
	}
	if got.Config.Dockerfile != "" || got.Config.Context != "" {
		t.Error("Dockerfile build fields must be cleared")
	}
	if len(got.Config.Features) != 0 {
		t.Error("Features must be cleared")
	}
	if source.Dockerfile != "Dockerfile" {
		t.Error("source config must not be mutated")
	}
}

func TestDefaultEntrypointSingleLine(t *testing.T) {
	if strings.Contains(DefaultEntrypoint, "\n") {
		t.Fatalf("DefaultEntrypoint must be single-line, got %q", DefaultEntrypoint)
	}
	if !strings.Contains(DefaultEntrypoint, "internal agent container daemon") {
		t.Errorf("DefaultEntrypoint must invoke the agent daemon, got %q", DefaultEntrypoint)
	}
	if !strings.Contains(DefaultEntrypoint, `"${DEVSY_AGENT_PATH:-/usr/local/bin/devsy}"`) {
		t.Errorf("DefaultEntrypoint must honor DEVSY_AGENT_PATH, got %q", DefaultEntrypoint)
	}
}

func TestGetStartScriptSingleLine(t *testing.T) {
	merged := &config.MergedDevContainerConfig{
		UpdatedConfigProperties: config.UpdatedConfigProperties{
			Entrypoints: []string{"echo setup"},
		},
	}
	got := GetStartScript(merged)
	if strings.Contains(got, "\n") {
		t.Fatalf("GetStartScript() must be single-line, got %q", got)
	}
	if !strings.Contains(got, `exec "$@"`) {
		t.Fatalf("GetStartScript() must keep the shell exec passthrough, got %q", got)
	}
	if !strings.Contains(got, "internal agent container daemon") {
		t.Fatalf("GetStartScript() must invoke the agent, got %q", got)
	}
}

func TestGetStartScriptPreservesStatementOrder(t *testing.T) {
	merged := &config.MergedDevContainerConfig{
		UpdatedConfigProperties: config.UpdatedConfigProperties{
			Entrypoints: []string{"first-entrypoint", "second-entrypoint"},
		},
	}
	got := GetStartScript(merged)
	wantOrder := []string{
		startScriptEchoStatement,
		startScriptTrapStatement,
		`exec "$@"`,
		"first-entrypoint",
		"second-entrypoint",
		"internal agent container daemon",
	}
	lastIdx := -1
	for _, want := range wantOrder {
		idx := strings.Index(got, want)
		if idx == -1 {
			t.Fatalf("expected %q in script, got %q", want, got)
		}
		if idx <= lastIdx {
			t.Fatalf("expected %q to appear after previous statement in %q", want, got)
		}
		lastIdx = idx
	}
}

func TestGetStartScriptOmitsEmptyStatementForNoCustomEntrypoints(t *testing.T) {
	got := GetStartScript(&config.MergedDevContainerConfig{})
	if strings.Contains(got, ";;") || strings.Contains(got, "; ;") {
		t.Errorf(
			"expected no empty statement between built-ins and default entrypoint, got %q",
			got,
		)
	}
}

func TestIsRecoveryContainer(t *testing.T) {
	if isRecoveryContainer(nil) {
		t.Error("nil details must not be a recovery container")
	}

	plain := &config.ContainerDetails{}
	if isRecoveryContainer(plain) {
		t.Error("container without the recovery label must not be flagged")
	}

	recovery := &config.ContainerDetails{
		Config: config.ContainerDetailsConfig{
			Labels: map[string]string{pkgconfig.DockerRecoveryLabel: pkgconfig.LabelValueTrue},
		},
	}
	if !isRecoveryContainer(recovery) {
		t.Error("container with the recovery label must be flagged")
	}
}

type migrationMockDriver struct {
	*provisioningPreflightMockDriver
	required       bool
	migrationCalls int
}

func (d *migrationMockDriver) RequiresRecreate(*config.ContainerDetails) (bool, string) {
	d.migrationCalls++
	return d.required, "workspace mount contract changed"
}

func TestDriverMountContractMigration(t *testing.T) {
	for _, tt := range []struct {
		name                                                  string
		required, explicit, external, wantRecreate, wantError bool
		calls                                                 int
	}{
		{"incompatible", true, false, false, true, false, 1},
		{"compatible", false, false, false, false, false, 1},
		{"explicit", true, true, false, true, false, 0},
		{"external", true, false, true, false, true, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := &migrationMockDriver{
				provisioningPreflightMockDriver: &provisioningPreflightMockDriver{
					mockDriver: &mockDriver{},
				},
				required: tt.required,
			}
			r := newTestRunner(d)
			params := recreateResolveParams()
			params.options.Recreate = tt.explicit
			if tt.external {
				params.parsedConfig.Config.ContainerID = testContainerID
			}
			err := r.applyDriverRecreateRequirement(runningContainerDetails(), params)
			if tt.wantError {
				require.ErrorContains(t, err, "cannot migrate externally managed container")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.wantRecreate, params.options.Recreate)
			require.Equal(t, tt.calls, d.migrationCalls)
			require.False(t, d.stopCalled)
			require.False(t, d.deleteCalled)
		})
	}
}

func TestAutomaticMigrationPreflightFailurePreservesExisting(t *testing.T) {
	sentinel := errors.New("unsupported provisioning runtime")
	d := &migrationMockDriver{
		provisioningPreflightMockDriver: &provisioningPreflightMockDriver{
			mockDriver:      &mockDriver{},
			provisioningErr: sentinel,
		},
		required: true,
	}
	params := recreateResolveParams()
	params.options.Recreate = false
	_, err := newTestRunner(
		d,
	).resolveContainer(context.Background(), params, runningContainerDetails())
	require.ErrorIs(t, err, sentinel)
	require.True(t, d.provisioningCalled)
	require.True(t, params.options.Recreate)
	require.False(t, d.stopCalled)
	require.False(t, d.deleteCalled)
}

func TestEffectiveRemoteUser(t *testing.T) {
	for _, tt := range []struct{ remote, container, want string }{
		{"vscode", containerRootUser, "vscode"}, {"", "node", "node"}, {"", "", containerRootUser},
	} {
		cfg := &config.MergedDevContainerConfig{}
		cfg.RemoteUser = tt.remote
		require.Equal(t, tt.want, effectiveRemoteUser(cfg, tt.container))
	}
}

func TestRunOptionsSeparateDeveloperIdentity(t *testing.T) {
	r := newTestRunner(&mockDriver{})
	cfg := &config.MergedDevContainerConfig{}
	cfg.ContainerUser = containerRootUser
	cfg.RemoteUser = "vscode"
	info := &config.BuildInfo{
		ImageName:     "final-image",
		ImageMetadata: &config.ImageMetadataConfig{},
		ImageDetails:  &config.ImageDetails{},
	}
	info.ImageDetails.Config.User = "node"
	options, err := r.getRunOptions(cfg, &config.SubstitutionContext{}, info, false)
	require.NoError(t, err)
	require.Equal(t, containerRootUser, options.User)
	require.Equal(t, "vscode", options.RemoteUser)
	info.Dockerless = &config.BuildInfoDockerless{User: "node"}
	options, err = r.getDockerlessRunOptions(cfg, &config.SubstitutionContext{}, info, false)
	require.NoError(t, err)
	require.Equal(t, containerRootUser, options.User)
	require.Equal(t, "vscode", options.RemoteUser)
}
