//go:build windows

package audit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenRefusesOnWindows(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	_, err := Open(dir)
	require.ErrorIs(t, err, errWindowsUnsupported)
	require.NoDirExists(t, dir, "a refused Open must not create the directory")
}

func TestHostIDRefusesOnWindows(t *testing.T) {
	dir := t.TempDir()
	_, err := LoadOrCreateHostID(filepath.Join(dir, "host-id"))
	require.ErrorIs(t, err, errWindowsUnsupported)
	entries, readErr := os.ReadDir(dir)
	require.NoError(t, readErr)
	require.Empty(t, entries, "a refused host ID call must not write files")
}
