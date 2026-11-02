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
