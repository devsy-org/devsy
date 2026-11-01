//go:build !linux && !darwin

package dependencies

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMetadataWriterUnsupported(t *testing.T) {
	path := t.TempDir()
	root, err := os.OpenRoot(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, root.Close()) }()
	_, err = newMetadataWriter(root)
	require.ErrorContains(t, err, "unsupported")
	entries, err := os.ReadDir(path)
	require.NoError(t, err)
	require.Empty(t, entries)
}
