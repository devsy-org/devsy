//go:build !windows

package ssh

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteFileAtomicPreservesGroup(t *testing.T) {
	groups, err := os.Getgroups()
	require.NoError(t, err)

	current := os.Getegid()
	other := -1
	for _, gid := range groups {
		if gid != current {
			other = gid
			break
		}
	}
	if other < 0 {
		t.Skip("no supplementary group available")
	}

	target := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(target, []byte("old"), 0o600))
	require.NoError(t, os.Chown(target, -1, other))

	require.NoError(t, writeFileAtomic(target, []byte("new")))

	info, err := os.Stat(target)
	require.NoError(t, err)
	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok)
	require.Equal(t, other, int(stat.Gid))
	data, err := os.ReadFile(filepath.Clean(target))
	require.NoError(t, err)
	require.Equal(t, "new", string(data))
}

func TestChownLikeMissingTargetIsNoop(t *testing.T) {
	temp, err := os.CreateTemp(t.TempDir(), "owner")
	require.NoError(t, err)
	t.Cleanup(func() { _ = temp.Close() })

	require.NoError(t, chownLike(filepath.Join(t.TempDir(), "absent"), temp))
}

func TestChownLikeSameOwnerIsNoop(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o600))
	temp, err := os.CreateTemp(dir, "owner")
	require.NoError(t, err)
	t.Cleanup(func() { _ = temp.Close() })

	require.NoError(t, chownLike(target, temp))
}

func foreignOwnedPath(t *testing.T) string {
	t.Helper()

	if os.Geteuid() == 0 {
		t.Skip("root can chown to any owner")
	}
	info, err := os.Stat(string(filepath.Separator))
	require.NoError(t, err)
	owner, ok := fileOwner(info)
	require.True(t, ok)
	if int(owner[0]) == os.Geteuid() {
		t.Skip("filesystem root is owned by the current user")
	}

	return string(filepath.Separator)
}

func TestChownLikeFailsWhenForeignOwnerCannotBePreserved(t *testing.T) {
	target := foreignOwnedPath(t)
	temp, err := os.CreateTemp(t.TempDir(), "owner")
	require.NoError(t, err)
	t.Cleanup(func() { _ = temp.Close() })

	err = chownLike(target, temp)
	require.ErrorIs(t, err, errOwnerMismatch)
	require.ErrorIs(t, preserveConfigOwner(target, temp), errOwnerMismatch)
}
