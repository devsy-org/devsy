package agent

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const stateTestRoot = "/state"

func TestStateLocationWorkspaceConfigPattern(t *testing.T) {
	tests := []struct {
		name     string
		location StateLocation
		want     string
		wantErr  string
	}{
		{
			name:     "canonical",
			location: StateLocation{Root: stateTestRoot, Layout: StateLayoutCanonical},
			want: filepath.Join(
				"/state",
				"contexts",
				"*",
				"workspaces",
				"*",
				"agent",
				"workspace.json",
			),
		},
		{
			name:     "agent home",
			location: StateLocation{Root: stateTestRoot, Layout: StateLayoutAgentHome},
			want:     filepath.Join("/state", "contexts", "*", "workspaces", "*", "workspace.json"),
		},
		{
			name:     "relative root",
			location: StateLocation{Root: "state", Layout: StateLayoutCanonical},
			wantErr:  "must be absolute",
		},
		{
			name:     "unknown layout",
			location: StateLocation{Root: stateTestRoot, Layout: "other"},
			wantErr:  "unsupported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.location.WorkspaceConfigPattern()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveStateLocation(t *testing.T) {
	t.Run("explicit agent data path", func(t *testing.T) {
		location, err := ResolveStateLocation(ResolveStateLocationOptions{
			AgentDataPath: "/state/agent/../agent",
		})
		require.NoError(t, err)
		assert.Equal(t, StateLocation{Root: "/state/agent", Layout: StateLayoutAgentHome}, location)
	})

	t.Run("canonical origin", func(t *testing.T) {
		location, err := ResolveStateLocation(ResolveStateLocationOptions{
			Origin:      "/state/contexts/default/workspaces/workspace-id/agent",
			Context:     "default",
			WorkspaceID: "workspace-id",
		})
		require.NoError(t, err)
		assert.Equal(t, StateLocation{Root: "/state", Layout: StateLayoutCanonical}, location)
	})

	t.Run("malformed origin", func(t *testing.T) {
		_, err := ResolveStateLocation(ResolveStateLocationOptions{
			Origin:      "/state/contexts/default/workspaces/workspace-id",
			Context:     "default",
			WorkspaceID: "workspace-id",
		})
		require.ErrorContains(t, err, "unexpected canonical workspace origin")
	})
}
