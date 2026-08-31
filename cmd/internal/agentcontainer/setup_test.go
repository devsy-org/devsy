//go:build !windows

package agentcontainer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsGitPermissionDenied_ReadOnlyFileSystem(t *testing.T) {
	err := &git.CommandError{
		Stderr: "fatal: could not write config: Read-only file system",
	}
	require.True(t, isGitPermissionDenied(err))
}

func TestSkipSnapshotRestore_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, skipSnapshotRestore(dir))
}

func TestSkipSnapshotRestore_OnlySynthesizedDevContainer(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, synthesizedDevContainerName), []byte("{}"), 0o600,
	))
	assert.False(
		t, skipSnapshotRestore(dir),
		"the synthesized devcontainer.json alone must not count as existing content",
	)
}

func TestSkipSnapshotRestore_RealContentPresent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, synthesizedDevContainerName), []byte("{}"), 0o600,
	))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("x"), 0o600))
	assert.True(t, skipSnapshotRestore(dir))
}

func TestSkipSnapshotRestore_MissingDir(t *testing.T) {
	assert.False(t, skipSnapshotRestore(filepath.Join(t.TempDir(), "missing")))
}
