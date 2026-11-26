package agent

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceInitializationOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, oldUID string
		existing     bool
		deletions    int
	}{
		{name: "new workspace"},
		{name: "same UID", oldUID: "current-uid", existing: true},
		{name: "replaced UID", oldUID: "previous-uid", deletions: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			info := &provider.AgentWorkspaceInfo{
				Workspace: &provider.Workspace{
					Context: config.DefaultContext,
					ID:      "ownership",
					UID:     "current-uid",
				},
				Agent: provider.ProviderAgentConfig{DataPath: home, Local: config.BoolTrue},
			}
			origin, err := CreateAgentWorkspaceDir(home, info.Workspace.Context, info.Workspace.ID)
			require.NoError(t, err)
			if tc.oldUID != "" {
				old := provider.CloneAgentWorkspaceInfo(info)
				old.Workspace.UID = tc.oldUID
				old.Origin = origin
				require.NoError(t, PersistAgentWorkspaceInfo(old))
			}
			data, err := json.Marshal(info)
			require.NoError(t, err)
			var input map[string]any
			require.NoError(t, json.Unmarshal(data, &input))
			input["WorkspaceWasExisting"] = true
			data, err = json.Marshal(input)
			require.NoError(t, err)
			encoded, err := compress.Compress(string(data))
			require.NoError(t, err)
			deletions := 0
			shouldExit, loaded, err := WriteWorkspaceInfoAndDeleteOld(
				encoded,
				func(old *provider.AgentWorkspaceInfo) error {
					deletions++
					require.Equal(t, tc.oldUID, old.Workspace.UID)
					return os.RemoveAll(old.Origin)
				},
			)
			require.NoError(t, err)
			require.False(t, shouldExit)
			require.Equal(t, tc.deletions, deletions)
			require.Equal(t, tc.existing, loaded.WorkspaceWasExisting)
			require.Equal(
				t,
				tc.existing,
				provider.CloneAgentWorkspaceInfo(loaded).WorkspaceWasExisting,
			)
			data, err = json.Marshal(loaded)
			require.NoError(t, err)
			require.NotContains(t, string(data), "WorkspaceWasExisting")
		})
	}
}
