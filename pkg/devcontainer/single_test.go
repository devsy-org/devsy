package devcontainer

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
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
