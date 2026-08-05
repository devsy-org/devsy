//go:build linux || darwin || unix

package daemon

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListen_SocketPermissionsAreOwnerOnly(t *testing.T) {
	// Unix socket paths have a short max length (~104 bytes on macOS), so
	// this uses /tmp directly rather than t.TempDir()'s longer path.
	addr := fmt.Sprintf("/tmp/devsy-test-%d.sock", os.Getpid())
	t.Cleanup(func() { _ = os.Remove(addr) })

	pipe, err := listen(addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pipe.Close() })

	info, err := os.Stat(addr)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
