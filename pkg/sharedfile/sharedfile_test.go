package sharedfile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureMode_CreatesMissingFileAtMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")

	require.NoError(t, EnsureMode(path, 0o666))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o666), info.Mode().Perm())
}

func TestEnsureMode_WidensExistingRestrictiveFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0o600))

	require.NoError(t, EnsureMode(path, 0o666))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o666), info.Mode().Perm())
	//nolint:gosec // test-owned temp path
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content), "widening mode must not touch existing content")
}

func TestCreateIfMissing_LeavesExistingFileUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	//nolint:gosec // test fixture, intentional
	require.NoError(t, os.WriteFile(path, []byte("existing"), 0o644))

	require.NoError(t, createIfMissing(path, 0o666))

	//nolint:gosec // test-owned temp path
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "existing", string(content),
		"createIfMissing must not truncate a file a concurrent creator just wrote")
}

func TestWidenIfNeeded_SkipsChmodWhenModeAlreadyCorrect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	//nolint:gosec // test fixture, intentional
	require.NoError(t, os.WriteFile(path, nil, 0o666))
	//nolint:gosec // test fixture, intentional
	require.NoError(t, os.Chmod(path, 0o666))

	needsChmod, err := needsChmod(path, 0o666)
	require.NoError(t, err)
	assert.False(t, needsChmod,
		"a file already at the target mode must not need a chmod, since a "+
			"non-owning acquirer's chmod would fail with EPERM")

	require.NoError(t, WidenIfNeeded(path, 0o666))
}

func TestNeedsChmod(t *testing.T) {
	tests := []struct {
		name   string
		mode   os.FileMode
		want   os.FileMode
		expect bool
	}{
		{name: "already matches", mode: 0o666, want: 0o666, expect: false},
		{name: "narrower mode needs widening", mode: 0o644, want: 0o666, expect: true},
		{name: "wider mode needs narrowing", mode: 0o777, want: 0o666, expect: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "coord")
			require.NoError(t, os.WriteFile(path, nil, tc.mode))
			//nolint:gosec // test fixture, intentional
			require.NoError(t, os.Chmod(path, tc.mode))

			got, err := needsChmod(path, tc.want)
			require.NoError(t, err)
			assert.Equal(t, tc.expect, got)
		})
	}
}

func TestNeedsChmod_StatErrorPropagates(t *testing.T) {
	_, err := needsChmod(filepath.Join(t.TempDir(), "does-not-exist"), 0o666)
	require.Error(t, err)
}

func TestNeedsChmod_RejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	require.NoError(t, os.WriteFile(target, nil, 0o600))
	require.NoError(t, os.Symlink(target, link))

	_, err := needsChmod(link, 0o666)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")
}

func TestWidenIfNeeded_RejectsSymlinkWithoutTouchingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	require.NoError(t, os.WriteFile(target, nil, 0o600))
	require.NoError(t, os.Symlink(target, link))

	err := WidenIfNeeded(link, 0o666)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")

	info, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"the symlink target's mode must be untouched, not widened")
}
