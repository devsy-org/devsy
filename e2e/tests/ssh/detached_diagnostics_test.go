package ssh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/require"
)

func TestReadDiagnosticStreamsLimitsOutput(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "worker.streams")
	content := "complete line\n" + strings.Repeat("x", 64*1024)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	got, err := readDiagnosticStreams(home, path)
	require.NoError(t, err)
	require.Equal(t, "complete line\n", string(got))
	_, err = readDiagnosticStreams(home, filepath.Join(t.TempDir(), "outside"))
	require.Error(t, err)
}

func TestNewDetachedTaskExcludesExistingAndAmbiguousTasks(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
	source := filepath.Join(home, "project")
	store, err := task.NewStore()
	require.NoError(t, err)
	existing, err := store.Create(
		task.CreateOptions{Command: "up", WorkspaceID: workspace.ToID(source)},
	)
	require.NoError(t, err)
	before := map[string]struct{}{existing.ID(): {}}
	_, err = store.Create(task.CreateOptions{Command: "up", WorkspaceID: "unrelated"})
	require.NoError(t, err)
	created, err := store.Create(
		task.CreateOptions{Command: "up", WorkspaceID: workspace.ToID(source)},
	)
	require.NoError(t, err)
	got := newDetachedTask(before, source)
	require.NotNil(t, got)
	require.Equal(t, created.ID(), got.ID)
	_, err = store.Create(task.CreateOptions{Command: "up", WorkspaceID: workspace.ToID(source)})
	require.NoError(t, err)
	require.Nil(t, newDetachedTask(before, source))
}
