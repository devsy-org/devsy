package audit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenReaderRefusesWindows(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	for _, path := range []string{dir, missing} {
		r, err := OpenReader(path)
		require.ErrorIs(t, err, errReaderUnsupportedOS)
		require.Nil(t, r)
	}
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Empty(t, entries)
}
