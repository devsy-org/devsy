package machinediagnostics

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimePaths(t *testing.T) {
	assertPaths := func(t *testing.T, got RuntimePaths, dir string) {
		t.Helper()
		require.Equal(t, filepath.Join(dir, RuntimeLockFileName), got.LockPath)
		require.Equal(t, filepath.Join(dir, RuntimeLocatorFileName), got.LocatorPath)
	}

	assertPaths(t, SystemRuntimePaths(), DefaultRuntimeDir)
	paths, err := UserRuntimePaths(func() (string, error) { return "/tmp/test-cache", nil })
	require.NoError(t, err)
	assertPaths(t, paths, "/tmp/test-cache/devsy")

	_, err = UserRuntimePaths(func() (string, error) { return "", errors.New("unavailable") })
	require.ErrorContains(t, err, "get user cache directory for daemon runtime")
}

func TestEnsureRuntimeDirRepairsSharedPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	err := os.Chmod(dir, 0o750) //nolint:gosec // regression fixture.
	require.NoError(t, err)
	require.NoError(t, EnsureRuntimeDir(dir, true))

	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestEnsureRuntimeDirKeepsUserRuntimePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "runtime")
	require.NoError(t, EnsureRuntimeDir(dir, false))

	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.Zero(
		t,
		info.Mode().Perm()&^os.FileMode(0o750),
		"user runtime dir must not be world-accessible",
	)
}
