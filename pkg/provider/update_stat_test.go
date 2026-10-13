package provider

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStatUpdatePath(t *testing.T) {
	base := t.TempDir()
	requireUpdateStatSymlinks(t, base)
	owned := filepath.Join(base, "owned")
	nested := filepath.Join(owned, "nested")
	require.NoError(t, os.MkdirAll(filepath.Join(nested, "sub"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(owned, "file"), []byte("owned"), 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(owned, "directory"), 0o700))
	require.NoError(t, os.Symlink(".", filepath.Join(nested, "dot")))
	require.NoError(
		t,
		os.Symlink("sub"+string(filepath.Separator)+"..", filepath.Join(nested, "cancelled")),
	)
	require.NoError(t, os.Symlink("../file", filepath.Join(nested, "regular")))
	require.NoError(t, os.Symlink("../directory", filepath.Join(nested, "directory-link")))

	outside := filepath.Join(base, "outside")
	require.NoError(t, os.Mkdir(outside, 0o700))
	sentinel := filepath.Join(outside, "sentinel")
	require.NoError(t, os.WriteFile(sentinel, []byte("untouched"), 0o600))
	require.NoError(t, os.Symlink("../../outside", filepath.Join(nested, "escape")))
	require.NoError(t, os.Symlink("cycle-b", filepath.Join(nested, "cycle-a")))
	require.NoError(t, os.Symlink("cycle-a", filepath.Join(nested, "cycle-b")))

	root, err := os.OpenRoot(owned)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })

	t.Run("nested_dot_directory", func(t *testing.T) {
		info, err := StatUpdatePath(root, filepath.Join("nested", "dot"))
		require.NoError(t, err)
		require.True(t, info.IsDir())
	})
	t.Run("nested_cancelled_child", func(t *testing.T) {
		info, err := StatUpdatePath(root, filepath.Join("nested", "cancelled"))
		require.NoError(t, err)
		require.True(t, info.IsDir())
	})
	t.Run("regular_file", func(t *testing.T) {
		info, err := StatUpdatePath(root, filepath.Join("nested", "regular"))
		require.NoError(t, err)
		require.True(t, info.Mode().IsRegular())
		require.Equal(t, int64(len("owned")), info.Size())
	})
	t.Run("directory_target", func(t *testing.T) {
		info, err := StatUpdatePath(root, filepath.Join("nested", "directory-link"))
		require.NoError(t, err)
		require.True(t, info.IsDir())
	})
	t.Run("external_target", func(t *testing.T) {
		_, err := StatUpdatePath(root, filepath.Join("nested", "escape"))
		require.Error(t, err)
		// #nosec G304 -- Sentinel belongs to this test's private temporary directory.
		actual, readErr := os.ReadFile(sentinel)
		require.NoError(t, readErr)
		require.Equal(t, []byte("untouched"), actual)
	})
	t.Run("cycle", func(t *testing.T) {
		_, err := StatUpdatePath(root, filepath.Join("nested", "cycle-a"))
		require.Error(t, err)
	})
}

func requireUpdateStatSymlinks(t *testing.T, base string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		probe := filepath.Join(base, "symlink-capability")
		err := os.Symlink(".", probe)
		// ERROR_PRIVILEGE_NOT_HELD is Windows errno 1314; other setup errors must fail.
		const privilegeNotHeld = syscall.Errno(1314)
		if errors.Is(err, privilegeNotHeld) {
			t.Skipf("Windows account cannot create symbolic links: %v", err)
		}
		require.NoError(t, err)
		require.NoError(t, os.Remove(probe))
	}
}
