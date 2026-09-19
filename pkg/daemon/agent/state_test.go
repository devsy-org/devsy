package agent

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	stateTestRoot        = "/state"
	stateTestContext     = "default"
	stateTestWorkspaceID = "workspace-id"
)

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
			Origin: filepath.Join(
				stateTestRoot,
				stateContextsDir,
				stateTestContext,
				stateWorkspacesDir,
				stateTestWorkspaceID,
				"agent",
			),
			Context:     stateTestContext,
			WorkspaceID: stateTestWorkspaceID,
		})
		require.NoError(t, err)
		assert.Equal(t, StateLocation{Root: "/state", Layout: StateLayoutCanonical}, location)
	})

	t.Run("agent home origin", func(t *testing.T) {
		location, err := ResolveStateLocation(ResolveStateLocationOptions{
			Origin: filepath.Join(
				stateTestRoot,
				"agent",
				stateContextsDir,
				stateTestContext,
				stateWorkspacesDir,
				stateTestWorkspaceID,
			),
			Context:     stateTestContext,
			WorkspaceID: stateTestWorkspaceID,
		})
		require.NoError(t, err)
		assert.Equal(t, StateLocation{Root: "/state/agent", Layout: StateLayoutAgentHome}, location)
	})

	t.Run("malformed origin", func(t *testing.T) {
		_, err := ResolveStateLocation(ResolveStateLocationOptions{
			Origin: filepath.Join(
				stateTestRoot,
				"agent",
				stateContextsDir,
				stateTestContext,
				stateWorkspacesDir,
				"other-id",
			),
			Context:     stateTestContext,
			WorkspaceID: stateTestWorkspaceID,
		})
		require.ErrorContains(t, err, "unexpected agent home workspace origin")
	})
}
