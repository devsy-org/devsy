package provider

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	devcontainerconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/require"
)

func setupTestHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
}

func TestSaveWorkspaceResult_RefusesWithoutConfig(t *testing.T) {
	setupTestHome(t)

	ws := &Workspace{ID: "ghost", Context: config.DefaultContext}
	err := SaveWorkspaceResult(ws, &devcontainerconfig.Result{})
	require.Error(t, err)

	// Must not leave an orphan dir behind.
	dir, dirErr := GetWorkspaceDir(ws.Context, ws.ID)
	require.NoError(t, dirErr)
	require.NoDirExists(t, dir)
}

func TestSaveWorkspaceResult_WritesWhenConfigExists(t *testing.T) {
	setupTestHome(t)

	ws := &Workspace{ID: "real", Context: config.DefaultContext}
	require.NoError(t, SaveWorkspaceConfig(ws))
	require.NoError(t, SaveWorkspaceResult(ws, &devcontainerconfig.Result{}))

	dir, err := GetWorkspaceDir(ws.Context, ws.ID)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dir, WorkspaceResultFile))
	require.NoError(t, err)
}
