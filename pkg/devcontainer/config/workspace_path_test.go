package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalWorkspacePathPreservesDescendantSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	alias := filepath.Join(t.TempDir(), "workspace")
	require.NoError(t, os.Symlink(root, alias))
	outside := t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "unsafe")))
	for _, spelling := range []string{root, alias} {
		canonicalRoot, candidate, pathErr := CanonicalWorkspacePath(
			alias,
			filepath.Join(spelling, "unsafe", "file"),
		)
		require.NoError(t, pathErr)
		require.Equal(t, root, canonicalRoot)
		require.Equal(t, filepath.Join(root, "unsafe", "file"), candidate)
	}
	_, _, err = CanonicalWorkspacePath(alias, filepath.Join(outside, "file"))
	require.ErrorContains(t, err, "outside workspace")
	_, _, err = CanonicalWorkspacePath(alias, alias+"-sibling/file")
	require.ErrorContains(t, err, "outside workspace")
}
