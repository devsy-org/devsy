package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/require"
)

const (
	cacheInitBlockedMode = "blocked"
	cacheInitSaveMode    = "save"
)

func TestPlatformCacheInitialization(t *testing.T) {
	for _, mode := range []string{"override", "native", "unresolved", cacheInitBlockedMode, cacheInitSaveMode} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			want := platformCacheProcessPaths(t, root, mode)
			t.Setenv("DEVSY_CLIENT_CACHE_TEST", mode)
			t.Setenv("DEVSY_CLIENT_CACHE_EXPECTED", want)
			before, err := dependencytest.Tree(root)
			require.NoError(t, err)
			binary, err := os.Executable()
			require.NoError(t, err)
			// Re-execution observes package init with fresh roots before any test runs.
			// #nosec G204 -- re-execute only this test binary with a fixed test selector.
			command := exec.CommandContext(
				t.Context(),
				binary,
				"-test.run=^TestPlatformCacheInitializationChild$",
			)
			output, err := command.CombinedOutput()
			require.NoError(t, err, "%s", output)
			if mode != cacheInitSaveMode {
				after, err := dependencytest.Tree(root)
				require.NoError(t, err)
				require.Equal(t, before, after)
			}
		})
	}
}

func platformCacheProcessPaths(t *testing.T, root, mode string) string {
	t.Helper()
	home := filepath.Join(root, "user")
	local := filepath.Join(root, "local")
	for key, value := range map[string]string{
		"HOME": home, "USERPROFILE": home, "LOCALAPPDATA": local,
		"APPDATA":        filepath.Join(root, "appdata"),
		"XDG_CACHE_HOME": filepath.Join(root, "xdg-cache-decoy"),
		"TMPDIR":         root, "TMP": root, "TEMP": root,
	} {
		t.Setenv(key, value)
	}
	t.Setenv(pkgconfig.EnvHome, "")
	switch mode {
	case "native":
		if runtime.GOOS == "windows" {
			return filepath.Join(local, pkgconfig.RepoName, "cache", "platform")
		}
		return filepath.Join(home, ".cache", pkgconfig.RepoName, "platform")
	case "unresolved":
		t.Setenv("HOME", "")
		t.Setenv("USERPROFILE", "")
		t.Setenv("LOCALAPPDATA", "")
		return filepath.Join(root, "devsy-platform")
	default:
		override := filepath.Join(root, "temporary home")
		if mode == cacheInitBlockedMode {
			require.NoError(t, os.WriteFile(override, []byte("not a directory"), 0o600))
		}
		t.Setenv(pkgconfig.EnvHome, override)
		return filepath.Join(override, "cache", "platform")
	}
}

func TestPlatformCacheInitializationChild(t *testing.T) {
	mode := os.Getenv("DEVSY_CLIENT_CACHE_TEST")
	if mode == "" {
		return
	}
	want := os.Getenv("DEVSY_CLIENT_CACHE_EXPECTED")
	require.Equal(t, want, CacheFolder)
	require.Equal(t, filepath.Join(want, "config.json"), DefaultCacheConfig)
	if mode == cacheInitBlockedMode {
		_, err := NewClientFromPath(DefaultCacheConfig)
		require.Error(t, err)
		return
	}
	c, err := NewClientFromPath(DefaultCacheConfig)
	require.NoError(t, err)
	require.Empty(t, c.Config().Host)
	if mode != cacheInitSaveMode {
		return
	}
	// LoginRaw persists credentials locally without opening a browser or contacting a host.
	require.NoError(t, c.LoginRaw("https://fixture.invalid", "fixture-key", false))
	require.FileExists(t, DefaultCacheConfig)
	reloaded, err := NewClientFromPath(DefaultCacheConfig)
	require.NoError(t, err)
	require.Equal(t, c.Config(), reloaded.Config())
	require.Equal(t, "Config", reloaded.Config().Kind)
	require.Equal(t, "storage.devsy.sh/v1", reloaded.Config().APIVersion)
	require.NoError(t, reloaded.Save())
}
