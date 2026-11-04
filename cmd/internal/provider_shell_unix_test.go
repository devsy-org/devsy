//go:build !windows

package cmdinternal

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func providerShellCLITestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(t.TempDir(), "private-")
	require.NoError(t, err)
	return dir
}
