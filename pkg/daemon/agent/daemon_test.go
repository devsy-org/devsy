package agent

import (
	"testing"

	"github.com/devsy-org/api/pkg/devsy"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

func TestBuildWorkspaceDaemonConfig_ShutdownAction(t *testing.T) {
	tests := []struct {
		name           string
		shutdownAction string
		want           string
	}{
		{
			name:           "defaults to stopContainer when empty",
			shutdownAction: "",
			want:           "stopContainer",
		},
		{
			name:           "preserves none",
			shutdownAction: "none",
			want:           "none",
		},
		{
			name:           "preserves stopContainer",
			shutdownAction: "stopContainer",
			want:           "stopContainer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged := &config.MergedDevContainerConfig{}
			merged.ShutdownAction = tt.shutdownAction

			cfg, err := BuildWorkspaceDaemonConfig(
				devsy.PlatformOptions{},
				&provider2.Workspace{},
				&config.SubstitutionContext{},
				merged,
			)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.ShutdownAction != tt.want {
				t.Errorf("ShutdownAction = %q, want %q", cfg.ShutdownAction, tt.want)
			}
		})
	}
}
