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
	content, err := os.ReadFile(path) //nolint:gosec // test-owned temp path
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content), "widening mode must not touch existing content")
}

func TestCreateIfMissing_LeavesExistingFileUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	//nolint:gosec // test fixture, intentional
	require.NoError(t, os.WriteFile(path, []byte("existing"), 0o644))

	require.NoError(t, createIfMissing(path, 0o666))

	content, err := os.ReadFile(path) //nolint:gosec // test-owned temp path
	require.NoError(t, err)
	assert.Equal(t, "existing", string(content),
		"createIfMissing must not truncate a file a concurrent creator just wrote")
}

func TestWidenIfNeeded_SkipsChmodWhenModeAlreadyCorrect(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	//nolint:gosec
	require.NoError(t, os.WriteFile(path, nil, 0o666))
	//nolint:gosec
	require.NoError(t, os.Chmod(path, 0o666))

	require.NoError(t, WidenIfNeeded(path, 0o666))
}

func TestWidenIfNeeded_SucceedsOnAlreadyCorrectModeWithoutWriteAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	//nolint:gosec // test fixture, intentional
	require.NoError(t, os.WriteFile(path, nil, 0o444))
	//nolint:gosec // test fixture, intentional
	require.NoError(t, os.Chmod(path, 0o444))

	require.NoError(t, WidenIfNeeded(path, 0o444),
		"confirming an already-correct mode must not require write access to the file")
}

func TestWidenIfNeeded_WidensNarrowerMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	require.NoError(t, os.WriteFile(path, nil, 0o644)) //nolint:gosec
	require.NoError(t, WidenIfNeeded(path, 0o666))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o666), info.Mode().Perm())
}

func TestWidenIfNeeded_StatErrorPropagates(t *testing.T) {
	err := WidenIfNeeded(filepath.Join(t.TempDir(), "does-not-exist"), 0o666)
	require.Error(t, err)
}

func TestWidenIfNeeded_RejectsSymlinkWithoutTouchingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	require.NoError(t, os.WriteFile(target, nil, 0o600))
	require.NoError(t, os.Symlink(target, link))

	err := WidenIfNeeded(link, 0o666)
	require.Error(t, err, "opening link with O_NOFOLLOW must fail (ELOOP), not follow to target")

	info, err := os.Stat(target)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"the symlink target's mode must be untouched, not widened")
}

func TestWidenIfNeeded_SymlinkSwappedAfterOpenCannotRedirectChmod(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "coord")
	decoyTarget := filepath.Join(dir, "decoy")
	require.NoError(t, os.WriteFile(path, nil, 0o644)) //nolint:gosec
	require.NoError(t, os.WriteFile(decoyTarget, nil, 0o600))

	f, err := os.OpenFile(path, os.O_WRONLY, 0) //nolint:gosec // test-owned temp path
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	require.NoError(t, os.Symlink(decoyTarget, path))

	require.NoError(t, f.Chmod(0o666), "chmod on an already-open fd must not be redirected")
	require.NoError(t, f.Close())

	decoyInfo, err := os.Stat(decoyTarget)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), decoyInfo.Mode().Perm(),
		"the symlink planted after open must not have been affected")
}

func TestReadFile_ReadsExistingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	//nolint:gosec // test fixture, intentional
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0o644))

	got, err := ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got))
}

func TestReadFile_RejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	require.NoError(t, os.WriteFile(target, []byte("secret"), 0o600))
	require.NoError(t, os.Symlink(target, link))

	_, err := ReadFile(link)
	require.Error(t, err)
}

func TestWriteFile_CreatesNewFileAtMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")

	require.NoError(t, WriteFile(path, []byte("hello"), 0o644))

	//nolint:gosec // test-owned temp path
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(content))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestWriteFile_OverwritesExistingContentAndWidensMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	// #nosec G306 -- test fixture, intentional
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))

	require.NoError(t, WriteFile(path, []byte("new"), 0o644))

	//nolint:gosec // test-owned temp path
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new", string(content))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

func TestWriteFile_RejectsSymlinkWithoutTouchingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	require.NoError(t, os.WriteFile(target, []byte("untouched"), 0o600))
	require.NoError(t, os.Symlink(target, link))

	err := WriteFile(link, []byte("attacker-controlled"), 0o644)
	require.Error(t, err)

	//nolint:gosec // test-owned temp path
	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "untouched", string(content))
}
