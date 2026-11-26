//go:build windows

package session

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoreAndLockRefuseOnWindows(t *testing.T) {
	root := filepath.Join(t.TempDir(), "pools")
	dir := filepath.Join(root, "pool", "sessions")
	store, err := OpenStore(dir, "Bad Pool", nil, Limits{})
	require.ErrorIs(t, err, errWindowsUnsupported)
	require.Nil(t, store)
	layout, err := NewLayout(root, "pool")
	require.NoError(t, err)
	lock, err := layout.Lock()
	require.ErrorIs(t, err, errWindowsUnsupported)
	require.Nil(t, lock)
	sess, err := (&Store{}).create()
	require.ErrorIs(t, err, errLockNotHeld)
	require.Zero(t, sess)
	require.NoDirExists(t, filepath.Join(root, "pool"), "a refused call creates no directory")
}
