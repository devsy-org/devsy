package cmdinternal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWidenSharedFileCmd_WidensModeArgument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	//nolint:gosec // test fixture, intentional
	require.NoError(t, os.WriteFile(path, nil, 0o644))

	cmd := NewWidenSharedFileCmd(&flags.GlobalFlags{})
	cmd.SetArgs([]string{path, "0666"})
	require.NoError(t, cmd.Execute())

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o666), info.Mode().Perm())
}

func TestWidenSharedFileCmd_RejectsInvalidMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	//nolint:gosec // test fixture, intentional
	require.NoError(t, os.WriteFile(path, nil, 0o644))

	cmd := NewWidenSharedFileCmd(&flags.GlobalFlags{})
	cmd.SetArgs([]string{path, "not-a-mode"})
	require.Error(t, cmd.Execute())
}
