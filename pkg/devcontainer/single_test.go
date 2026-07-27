package devcontainer

import (
	"testing"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/types"
)

const mountTypeVolume = "volume"

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
		t.Errorf("OverrideFeatureInstallOrder must be cleared, got %v", got.Config.OverrideFeatureInstallOrder)
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
		t.Errorf("Image = %q, want default recovery image %q", got.Config.Image, defaultRecoveryImage)
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
