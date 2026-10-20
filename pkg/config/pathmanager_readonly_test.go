package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/stretchr/testify/require"
)

type readOnlyTestRoots struct {
	config, data, cache, state, runtime, systemBin string
}

type readOnlyPathCase struct {
	name string
	fn   func() (string, error)
	want string
}

func TestReadOnlyPathManagerPlatformDefaults(t *testing.T) {
	root := t.TempDir()
	roots := setReadOnlyTestEnvironment(t, root)
	dependencytest.Preserve(t, root)

	assertReadOnlyPaths(t, NewReadOnlyPathManager(), roots)
}

func TestReadOnlyPathManagerHomeOverrides(t *testing.T) {
	root := t.TempDir()
	defaults := setReadOnlyTestEnvironment(t, root)
	t.Chdir(root)
	for _, home := range []string{
		filepath.Join(root, "Devsy \u65e5\u672c home"),
		root + string(filepath.Separator) + "unused/../lexical home/.",
		"./relative home/../\u914d\u7f6e/",
	} {
		t.Run(home, func(t *testing.T) {
			t.Setenv(EnvHome, home)
			dependencytest.Preserve(t, root)
			assertReadOnlyPaths(t, NewReadOnlyPathManager(), readOnlyTestRoots{
				config: filepath.Clean(home), data: filepath.Clean(home),
				cache: filepath.Join(home, "cache"), state: filepath.Join(home, "state"),
				runtime: filepath.Join(home, "run"), systemBin: defaults.systemBin,
			})
		})
	}
}

func TestReadOnlyPathManagerConfigOverride(t *testing.T) {
	root := t.TempDir()
	setReadOnlyTestEnvironment(t, root)
	blocked := filepath.Join(root, "file")
	require.NoError(t, os.WriteFile(blocked, []byte("unchanged"), 0o600))
	for _, home := range []string{"", blocked} {
		t.Run(home, func(t *testing.T) {
			t.Setenv(EnvHome, home)
			configFile := "./missing/../\u914d\u7f6e config.yaml"
			t.Setenv(EnvConfig, configFile)
			dependencytest.Preserve(t, root)
			for _, pm := range []PathManager{NewReadOnlyPathManager(), NewPathManager()} {
				got, err := pm.ConfigFilePath()
				require.NoError(t, err)
				require.Equal(t, configFile, got)
			}
		})
	}
}

func TestReadOnlyPathManagerFileParent(t *testing.T) {
	root := t.TempDir()
	defaults := setReadOnlyTestEnvironment(t, root)
	file := filepath.Join(root, "file parent")
	require.NoError(t, os.WriteFile(file, []byte("unchanged"), 0o600))
	t.Setenv(EnvHome, file)
	dependencytest.Preserve(t, root)

	assertReadOnlyPaths(t, NewReadOnlyPathManager(), readOnlyTestRoots{
		config: file, data: file, cache: filepath.Join(file, "cache"),
		state: filepath.Join(file, "state"), runtime: filepath.Join(file, "run"),
		systemBin: defaults.systemBin,
	})
}

func TestPathManagerCreationFailures(t *testing.T) {
	root := t.TempDir()
	setReadOnlyTestEnvironment(t, root)
	file := filepath.Join(root, "file parent")
	require.NoError(t, os.WriteFile(file, []byte("unchanged"), 0o600))
	t.Setenv(EnvHome, file)
	dependencytest.Preserve(t, root)
	pm := NewPathManager()
	paths := append(readOnlyCategoryCases(pm, readOnlyTestRoots{}), readOnlyDataCases(pm, "")...)
	paths = append(paths, readOnlyOtherCases(pm, readOnlyTestRoots{})...)
	for _, tt := range paths {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.fn()
			require.Empty(t, got)
			require.ErrorContains(t, err, "create directory ")
			var pathErr *os.PathError
			require.ErrorAs(t, err, &pathErr)
			require.Equal(t, "mkdir", pathErr.Op)
		})
	}
}

func TestPathManagerInstanceIsolation(t *testing.T) {
	root := t.TempDir()
	setReadOnlyTestEnvironment(t, root)
	home := filepath.Join(root, "Devsy \u65e5\u672c home")
	t.Setenv(EnvHome, home)
	ordinary := NewPathManager()
	defaultManager := DefaultPathManager()
	readOnly := NewReadOnlyPathManager()
	require.Same(t, defaultManager, DefaultPathManager())

	readOnlyPath, err := readOnly.DependencyLocksDir()
	require.NoError(t, err)
	require.NoDirExists(t, home)
	for _, pm := range []PathManager{ordinary, defaultManager, NewPathManager()} {
		for _, tt := range readOnlyCategoryCases(pm, readOnlyTestRoots{}) {
			path, err := tt.fn()
			require.NoError(t, err)
			require.DirExists(t, path)
		}
		path, err := pm.DependencyLocksDir()
		require.NoError(t, err)
		require.Equal(t, readOnlyPath, path)
		require.NoDirExists(t, path)
		require.NoError(t, os.RemoveAll(home))
		path, err = readOnly.DependencyLocksDir()
		require.NoError(t, err)
		require.Equal(t, readOnlyPath, path)
		require.NoDirExists(t, home)
	}
}

func TestPathManagerConcurrentInstanceIsolation(t *testing.T) {
	root := t.TempDir()
	setReadOnlyTestEnvironment(t, root)
	home := filepath.Join(root, "devsy")
	t.Setenv(EnvHome, home)
	ordinary, readOnly := NewPathManager(), NewReadOnlyPathManager()
	var workers sync.WaitGroup
	for range 10 {
		workers.Go(func() {
			path, err := ordinary.CacheDir()
			if err != nil || path != filepath.Join(home, "cache") {
				t.Errorf("ordinary CacheDir() = %q, %v", path, err)
			}
		})
		workers.Go(func() {
			path, err := readOnly.StateDir()
			if err != nil || path != filepath.Join(home, "state") {
				t.Errorf("read-only StateDir() = %q, %v", path, err)
			}
		})
	}
	workers.Wait()
	require.DirExists(t, filepath.Join(home, "cache"))
	require.NoDirExists(t, filepath.Join(home, "state"))
}

func setReadOnlyTestEnvironment(t *testing.T, root string) readOnlyTestRoots {
	t.Helper()
	home, temp := filepath.Join(root, "user home"), filepath.Join(root, "temp")
	t.Setenv(EnvHome, "")
	t.Setenv(EnvConfig, "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("TMPDIR", temp)
	t.Setenv("TMP", temp)
	t.Setenv("TEMP", temp)
	return readOnlyPlatformTestRoots(home, temp)
}

func assertReadOnlyPaths(t *testing.T, pm PathManager, roots readOnlyTestRoots) {
	t.Helper()
	paths := append(readOnlyCategoryCases(pm, roots), readOnlyDataCases(pm, roots.data)...)
	paths = append(paths, readOnlyPathCase{"SystemBinDir", pm.SystemBinDir, roots.systemBin})
	paths = append(paths, readOnlyOtherCases(pm, roots)...)
	for _, tt := range paths {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.fn()
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func readOnlyCategoryCases(pm PathManager, r readOnlyTestRoots) []readOnlyPathCase {
	return []readOnlyPathCase{
		{"configuration root", pm.ConfigDir, r.config},
		{"data root", pm.DataDir, r.data},
		{"cache root", pm.CacheDir, r.cache},
		{"state root", pm.StateDir, r.state},
		{"runtime root", pm.RuntimeDir, r.runtime},
	}
}

func readOnlyDataCases(pm PathManager, data string) []readOnlyPathCase {
	context := filepath.Join(data, "contexts", "ctx")
	provider := filepath.Join(context, "providers", "provider")
	return append([]readOnlyPathCase{
		{"ContextDir", func() (string, error) { return pm.ContextDir("ctx") }, context},
		{
			"MachinesDir",
			func() (string, error) { return pm.MachinesDir("ctx") },
			filepath.Join(context, "machines"),
		},
		{
			"MachineDir",
			func() (string, error) { return pm.MachineDir("ctx", "machine") },
			filepath.Join(context, "machines", "machine"),
		},
		{
			"ProvidersDir",
			func() (string, error) { return pm.ProvidersDir("ctx") },
			filepath.Dir(provider),
		},
		{
			"ProviderDir",
			func() (string, error) { return pm.ProviderDir("ctx", "provider") },
			provider,
		},
		{
			"ProviderBinariesDir",
			func() (string, error) { return pm.ProviderBinariesDir("ctx", "provider") },
			filepath.Join(provider, "binaries"),
		},
		{
			"ProviderDaemonDir",
			func() (string, error) { return pm.ProviderDaemonDir("ctx", "provider") },
			filepath.Join(provider, "daemon"),
		},
		{
			"PlatformInstancesDir",
			func() (string, error) { return pm.PlatformInstancesDir("ctx") },
			filepath.Join(context, "pro_instances"),
		},
		{
			"PlatformInstanceDir",
			func() (string, error) { return pm.PlatformInstanceDir("ctx", "host") },
			filepath.Join(context, "pro_instances", "host"),
		},
		{
			"LocksDir",
			func() (string, error) { return pm.LocksDir("ctx") },
			filepath.Join(context, "locks"),
		},
	}, readOnlyWorkspaceCases(pm, context)...)
}

func readOnlyWorkspaceCases(pm PathManager, context string) []readOnlyPathCase {
	ws := filepath.Join(context, "workspaces", "ws")
	return []readOnlyPathCase{
		{
			"WorkspacesDir",
			func() (string, error) { return pm.WorkspacesDir("ctx") },
			filepath.Dir(ws),
		},
		{"WorkspaceDir", func() (string, error) { return pm.WorkspaceDir("ctx", "ws") }, ws},
		{
			"WorkspaceAgentDir",
			func() (string, error) { return pm.WorkspaceAgentDir("ctx", "ws") },
			filepath.Join(ws, "agent"),
		},
		{
			"WorkspaceLogDir",
			func() (string, error) { return pm.WorkspaceLogDir("ctx", "ws") },
			filepath.Join(ws, "logs"),
		},
		{
			"WorkspaceContentsDir",
			func() (string, error) { return pm.WorkspaceContentsDir("ctx") },
			filepath.Join(context, "contents"),
		},
		{
			"WorkspaceContentDir",
			func() (string, error) { return pm.WorkspaceContentDir("ctx", "ws") },
			filepath.Join(context, "contents", "ws"),
		},
	}
}

func readOnlyOtherCases(pm PathManager, r readOnlyTestRoots) []readOnlyPathCase {
	return []readOnlyPathCase{
		{"ConfigFilePath", pm.ConfigFilePath, filepath.Join(r.config, ConfigFile)},
		{"DependencyToolsDir", pm.DependencyToolsDir, filepath.Join(r.data, "tools")},
		{
			"DependencyDownloadCacheDir",
			pm.DependencyDownloadCacheDir,
			filepath.Join(r.cache, "dependencies"),
		},
		{"DependencyStateDir", pm.DependencyStateDir, filepath.Join(r.state, "dependencies")},
		{
			"DependencyLocksDir",
			pm.DependencyLocksDir,
			filepath.Join(r.state, "dependencies", "locks"),
		},
		{"AgentCacheDir", pm.AgentCacheDir, filepath.Join(r.cache, "agents")},
		{
			"ProviderDownloadCacheDir",
			pm.ProviderDownloadCacheDir,
			filepath.Join(r.cache, "providers"),
		},
		{
			"FeatureCacheDir",
			func() (string, error) { return pm.FeatureCacheDir("hash") },
			filepath.Join(r.cache, "features", "hash"),
		},
		{"PlatformCacheDir", pm.PlatformCacheDir, filepath.Join(r.cache, "platform")},
		{"SSHKeysDir", pm.SSHKeysDir, filepath.Join(r.cache, "keys")},
		{"DaemonPIDFile", pm.DaemonPIDFile, filepath.Join(r.runtime, DaemonProcessName+".pid")},
		{"DaemonLockFile", pm.DaemonLockFile, filepath.Join(r.runtime, DaemonProcessName+".lock")},
		{
			"DaemonStreamsFile",
			pm.DaemonStreamsFile,
			filepath.Join(r.runtime, DaemonProcessName+".streams"),
		},
		{
			"ProcessPIDFile",
			func() (string, error) { return pm.ProcessPIDFile("proc") },
			filepath.Join(r.runtime, "proc.pid"),
		},
		{
			"ProcessLockFile",
			func() (string, error) { return pm.ProcessLockFile("proc") },
			filepath.Join(r.runtime, "proc.lock"),
		},
		{
			"ProcessStreamsFile",
			func() (string, error) { return pm.ProcessStreamsFile("proc") },
			filepath.Join(r.runtime, "proc.streams"),
		},
		{"LogDir", pm.LogDir, filepath.Join(r.state, "logs")},
		{"TaskDir", pm.TaskDir, filepath.Join(r.state, "tasks")},
	}
}

func TestReadOnlyPathManagerPreservesExistingTree(t *testing.T) {
	root := t.TempDir()
	setReadOnlyTestEnvironment(t, root)
	home := filepath.Join(root, "existing home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "keep"), []byte("existing data"), 0o600))
	t.Setenv(EnvHome, home)
	dependencytest.Preserve(t, root)
	pm := NewReadOnlyPathManager()
	for _, tt := range readOnlyOtherCases(pm, readOnlyTestRoots{}) {
		_, err := tt.fn()
		require.NoError(t, err)
	}
	_, err := os.Stat(filepath.Join(home, "cache"))
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestReadOnlyPathManagerMissingEnvironment(t *testing.T) {
	root := t.TempDir()
	setReadOnlyTestEnvironment(t, root)
	for _, name := range []string{"HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(name, "")
	}
	dependencytest.Preserve(t, root)
	envName := "HOME"
	if runtime.GOOS == "windows" {
		envName = "APPDATA"
	}
	for _, pm := range []PathManager{NewReadOnlyPathManager(), NewPathManager()} {
		for _, fn := range []func() (string, error){
			pm.ConfigDir, pm.DataDir, pm.CacheDir, pm.StateDir, pm.ConfigFilePath,
		} {
			got, err := fn()
			require.Empty(t, got)
			require.ErrorContains(t, err, envName)
		}
	}
}
