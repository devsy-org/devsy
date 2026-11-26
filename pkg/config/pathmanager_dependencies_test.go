package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

type dependencyTestRoots struct {
	PathManager
	data, cache, state string
	err                error
}

func (p dependencyTestRoots) DataDir() (string, error)  { return p.data, p.err }
func (p dependencyTestRoots) CacheDir() (string, error) { return p.cache, p.err }
func (p dependencyTestRoots) StateDir() (string, error) { return p.state, p.err }

func TestDependencyPathsUseSharedRoots(t *testing.T) {
	root := t.TempDir()
	pm := &basePathManager{pm: dependencyTestRoots{
		data: filepath.Join(root, "data"), cache: filepath.Join(root, "cache"),
		state: filepath.Join(root, "state"),
	}}

	paths := []struct {
		name string
		fn   func() (string, error)
		want string
	}{
		{"tools", pm.DependencyToolsDir, filepath.Join(root, "data", "tools")},
		{"cache", pm.DependencyDownloadCacheDir, filepath.Join(root, "cache", "dependencies")},
		{"state", pm.DependencyStateDir, filepath.Join(root, "state", "dependencies")},
		{"locks", pm.DependencyLocksDir, filepath.Join(root, "state", "dependencies", "locks")},
	}
	for _, tt := range paths {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.fn()
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestDependencyPathsPropagateRootErrors(t *testing.T) {
	parentErr := errors.New("root unavailable")
	pm := &basePathManager{pm: dependencyTestRoots{err: parentErr}}
	for name, fn := range map[string]func() (string, error){
		"tools": pm.DependencyToolsDir, "cache": pm.DependencyDownloadCacheDir,
		"state": pm.DependencyStateDir, "locks": pm.DependencyLocksDir,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := fn()
			require.ErrorIs(t, err, parentErr)
			require.Empty(t, got)
		})
	}
}

func TestDependencyPathsPlatformDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))

	data := filepath.Join(home, "."+RepoName)
	cache := filepath.Join(home, ".cache", RepoName)
	if runtime.GOOS == "windows" {
		data = filepath.Join(home, "AppData", "Local", RepoName)
		cache = filepath.Join(data, "cache")
	}
	assertDependencyPaths(t, NewPathManager(), dependencyTestRoots{
		data: data, cache: cache, state: filepath.Join(data, "state"),
	})
}

func TestDependencyPathsDevsyHomeIsolation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	pm := NewPathManager()
	var previous []string
	for _, name := range []string{"first home", "second \u65e5\u672c\u8a9e home"} {
		t.Run(name, func(t *testing.T) {
			home := filepath.Join(root, name)
			t.Setenv(EnvHome, home)
			assertDependencyPaths(t, pm, dependencyTestRoots{
				data:  home,
				cache: filepath.Join(home, "cache"),
				state: filepath.Join(home, "state"),
			})
			paths := dependencyPaths(t, pm)
			for _, path := range paths {
				require.NotContains(t, previous, path)
			}
			previous = append(previous, paths...)
		})
	}
}

func assertDependencyPaths(t *testing.T, pm PathManager, roots dependencyTestRoots) {
	t.Helper()
	paths := dependencyPaths(t, pm)
	require.Equal(t, []string{
		filepath.Join(roots.data, "tools"),
		filepath.Join(roots.cache, "dependencies"),
		filepath.Join(
			roots.state,
			"dependencies",
		),
		filepath.Join(roots.state, "dependencies", "locks"),
	}, paths)
	for _, path := range paths {
		_, err := os.Stat(path)
		require.ErrorIs(t, err, fs.ErrNotExist, "dependency leaf must not be created: %s", path)
	}
	for _, parent := range []string{roots.data, roots.cache, roots.state} {
		info, err := os.Stat(parent)
		require.NoError(t, err)
		require.True(t, info.IsDir(), "existing category accessors ensure their roots")
	}
}

func dependencyPaths(t *testing.T, pm PathManager) []string {
	t.Helper()
	var paths []string
	for _, fn := range []func() (string, error){
		pm.DependencyToolsDir, pm.DependencyDownloadCacheDir, pm.DependencyStateDir, pm.DependencyLocksDir,
	} {
		path, err := fn()
		require.NoError(t, err)
		paths = append(paths, path)
	}
	return paths
}

func TestDependencyPathsPreserveExternalTreeAndEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(EnvHome, filepath.Join(home, "devsy"))
	external := filepath.Join(home, ".microsandbox")
	t.Setenv("MSB_HOME", external)
	require.NoError(t, os.MkdirAll(filepath.Join(external, "bin"), 0o700))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(external, "bin", "msb"), []byte("external executable"), 0o600),
	)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(external, "vm-data"), []byte("external data"), 0o600),
	)
	before := dependencyTestTree(t, external)
	env := os.Environ()

	pm := NewPathManager()
	assertDependencyPaths(t, pm, dependencyTestRoots{
		data: filepath.Join(home, "devsy"), cache: filepath.Join(home, "devsy", "cache"),
		state: filepath.Join(home, "devsy", "state"),
	})

	require.Equal(t, before, dependencyTestTree(t, external))
	require.Equal(t, env, os.Environ())
}

func dependencyTestTree(t *testing.T, root string) map[string]dependencyTestEntry {
	t.Helper()
	tree, err := os.OpenRoot(root)
	require.NoError(t, err)
	defer func() { require.NoError(t, tree.Close()) }()
	entries := map[string]dependencyTestEntry{}
	err = fs.WalkDir(tree.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		require.NoError(t, err)
		var content []byte
		if !entry.IsDir() {
			content, err = tree.ReadFile(path)
			require.NoError(t, err)
		}
		entries[path] = dependencyTestEntry{
			mode:    info.Mode(),
			size:    info.Size(),
			content: string(content),
		}
		return nil
	})
	require.NoError(t, err)
	return entries
}

type dependencyTestEntry struct {
	mode    fs.FileMode
	size    int64
	content string
}
