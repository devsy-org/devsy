//go:build !windows

package shell

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func providerPrivateTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(t.TempDir(), "private-")
	require.NoError(t, err)
	return dir
}

func TestProviderPrivateTestDirMode(t *testing.T) {
	dir := providerPrivateTestDir(t)
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer func() { _ = root.Close() }()
	info, err := root.Stat(".")
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func providerChildReaped(pid int) error {
	err := syscall.Kill(pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err == nil {
		return errors.New("owned child still exists after acknowledgement")
	}
	return fmt.Errorf("cannot check owned child after acknowledgement: %w", err)
}
