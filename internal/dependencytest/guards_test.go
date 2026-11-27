package dependencytest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEnvironmentDetectsMutation(t *testing.T) {
	t.Setenv("DEVSY_GUARD_SENTINEL", "before")
	before := Environment()
	require.Equal(t, before, Environment())
	t.Setenv("DEVSY_GUARD_SENTINEL", "after")
	require.NotEqual(t, before, Environment())
}

func TestTreeDetectsMutation(t *testing.T) {
	mutations := map[string]func(string) error{
		"content": func(path string) error { return os.WriteFile(path, []byte("other"), 0o600) },
		"size":    func(path string) error { return os.WriteFile(path, []byte("longer"), 0o600) },
		"path":    func(path string) error { return os.Rename(path, path+".renamed") },
		"delete":  os.Remove,
		"create":  func(path string) error { return os.WriteFile(path+".new", nil, 0o600) },
		"mode":    func(path string) error { return os.Chmod(path, 0o400) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "external")
			require.NoError(t, os.WriteFile(path, []byte("bytes"), 0o600))
			before, err := Tree(root)
			require.NoError(t, err)
			require.NoError(t, mutate(path))
			after, err := Tree(root)
			require.NoError(t, err)
			require.NotEqual(t, before, after)
		})
	}
}

func TestPreserveAllowsReads(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "external")
	require.NoError(t, os.WriteFile(path, []byte("bytes"), 0o600))
	Preserve(t, root)
	reader, err := os.OpenRoot(root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reader.Close()) })
	data, err := reader.ReadFile("external")
	require.NoError(t, err)
	require.Equal(t, "bytes", string(data))
}

func TestTreeDoesNotFollowLinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(outside, link))
	before, err := Tree(root)
	require.NoError(t, err)
	require.Len(t, before, 2)
	require.Equal(t, outside, before[1].Link)
	require.NoError(t, os.WriteFile(filepath.Join(outside, "unobserved"), []byte("data"), 0o600))
	after, err := Tree(root)
	require.NoError(t, err)
	require.Equal(t, before, after)
	_, err = Tree(link)
	require.Error(t, err)
	require.NoError(t, os.Remove(link))
	require.NoError(t, os.Symlink(filepath.Join(outside, "missing"), link))
	after, err = Tree(root)
	require.NoError(t, err)
	require.NotEqual(t, before, after)
}
