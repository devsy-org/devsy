package devcontainer

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

func TestWorkspaceMountDestination(t *testing.T) {
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
				{Type: "bind", Source: "/home/user/project", Destination: "/workspaces/my-app"},
			},
			want: "/workspaces/my-app",
		},
		{
			name: "volume mount under /workspaces/ is ignored",
			mounts: []config.ContainerMount{
				{Type: "volume", Source: "myvol", Destination: "/workspaces/other"},
			},
			want: "",
		},
		{
			name: "bind mount outside /workspaces/ is ignored",
			mounts: []config.ContainerMount{
				{Type: "bind", Source: "/host/path", Destination: "/app"},
			},
			want: "",
		},
		{
			name: "multiple mounts returns first workspace bind",
			mounts: []config.ContainerMount{
				{Type: "volume", Source: "cache", Destination: "/cache"},
				{Type: "bind", Source: "/home/user/ws", Destination: "/workspaces/old-name"},
				{Type: "bind", Source: "/tmp/extra", Destination: "/extra"},
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
