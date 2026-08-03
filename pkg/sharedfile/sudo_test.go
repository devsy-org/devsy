package sharedfile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWidenWithSudoFallback_NoOpsWhenFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist")

	err := WidenWithSudoFallback(context.Background(), path, 0o666, t.Logf)
	require.NoError(t, err, "a missing file is the caller's job to create, not this")
}

func TestWidenWithSudoFallback_NoOpsWhenModeAlreadyCorrect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	//nolint:gosec // test fixture, intentional
	require.NoError(t, os.Chmod(path, 0o666))

	err := WidenWithSudoFallback(context.Background(), path, 0o666, t.Logf)
	require.NoError(t, err)
}

func TestWidenWithSudoFallback_WidensOwnedFileWithoutSudo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	require.NoError(t, WidenWithSudoFallback(context.Background(), path, 0o666, t.Logf))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o666), info.Mode().Perm(),
		"a file this process owns must be widened directly, without needing sudo")
}
