//go:build unix

package client

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/stretchr/testify/require"
)

const (
	cachePermissionChild            = "DEVSY_CLIENT_PERMISSION_CHILD"
	cachePermissionNative           = "native"
	cachePermissionOverride         = "override"
	cachePermissionExisting         = "native-existing"
	cachePermissionOverrideExisting = "override-existing"
	cachePermissionCustom           = "custom"
	cachePermissionPrefix           = "custom-prefix"
	cachePermissionNested           = "custom-nested"
	cachePermissionUnresolved       = "unresolved"
	cachePermissionBlocked          = "blocked"
	cachePermissionUnwritable       = "unwritable"
)

type cachePermissionFixture struct {
	root, mode, platform string
	mask                 os.FileMode
}

func TestPlatformCacheSavePermissions(t *testing.T) {
	binary, err := os.Executable()
	require.NoError(t, err)
	for _, mask := range []os.FileMode{0o022, 0o002} {
		for _, mode := range []string{
			cachePermissionNative, cachePermissionOverride, cachePermissionExisting, cachePermissionOverrideExisting,
			cachePermissionCustom, cachePermissionPrefix, cachePermissionNested, cachePermissionUnresolved,
			cachePermissionBlocked, cachePermissionUnwritable,
		} {
			t.Run(fmt.Sprintf("%s/umask-%03o", mode, mask), func(t *testing.T) {
				fixture := newCachePermissionFixture(t, mode, mask)
				fixture.runChild(t, binary)
			})
		}
	}
}

func newCachePermissionFixture(t *testing.T, mode string, mask os.FileMode) cachePermissionFixture {
	t.Helper()
	root := t.TempDir()
	cachePermissionFixture{root: root}.makeDirectory(t, root, 0o755)
	pathMode := cachePermissionOverride
	switch mode {
	case cachePermissionNative, cachePermissionExisting:
		pathMode = cachePermissionNative
	case cachePermissionUnresolved:
		pathMode = cachePermissionUnresolved
	}
	platform := platformCacheProcessPaths(t, root, pathMode)
	t.Setenv("DEVSY_CLIENT_CACHE_EXPECTED", platform)
	fixture := cachePermissionFixture{root: root, mode: mode, platform: platform, mask: mask}
	if mode != cachePermissionUnresolved {
		generic := filepath.Dir(filepath.Dir(platform))
		fixture.makeDirectory(t, generic, 0o755)
	}
	fixture.prepareExisting(t)
	t.Setenv(cachePermissionChild, mode)
	t.Setenv("DEVSY_CLIENT_PERMISSION_ROOT", root)
	t.Setenv("DEVSY_CLIENT_PERMISSION_MASK", strconv.FormatUint(uint64(mask), 10))
	return fixture
}

// makeDirectory confines deliberately traversable permission fixtures to the test root.
func (f cachePermissionFixture) makeDirectory(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	root, err := os.OpenRoot(f.root)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	relative, err := filepath.Rel(f.root, path)
	require.NoError(t, err)
	require.NoError(t, root.MkdirAll(relative, mode))
	require.NoError(t, root.Chmod(relative, mode))
}

func (f cachePermissionFixture) prepareExisting(t *testing.T) {
	t.Helper()
	category := filepath.Dir(f.platform)
	switch f.mode {
	case cachePermissionExisting, cachePermissionOverrideExisting:
		f.makeDirectory(t, category, 0o751)
		f.makeDirectory(t, f.platform, 0o750)
		require.NoError(
			t,
			os.WriteFile(filepath.Join(f.platform, "config.json"), []byte("{}"), 0o600),
		)
	case cachePermissionBlocked:
		require.NoError(t, os.WriteFile(category, []byte("blocked"), 0o600))
	case cachePermissionUnwritable:
		f.makeDirectory(t, category, 0o500)
		t.Cleanup(func() { f.makeDirectory(t, category, 0o700) })
	}
}

func (f cachePermissionFixture) runChild(t *testing.T, binary string) {
	t.Helper()
	before, err := dependencytest.Tree(f.root)
	require.NoError(t, err)
	// Re-execute the test binary resolved once by the parent, with a fixed selector.
	command := exec.CommandContext(
		t.Context(),
		binary,
		"-test.run=^TestPlatformCacheSavePermissionsChild$",
	)
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	if f.mode == cachePermissionBlocked || f.mode == cachePermissionUnwritable {
		after, err := dependencytest.Tree(f.root)
		require.NoError(t, err)
		require.Equal(t, before, after)
	}
}

func TestPlatformCacheSavePermissionsChild(t *testing.T) {
	mode := os.Getenv(cachePermissionChild)
	if mode == "" {
		return
	}
	mask, err := strconv.ParseUint(os.Getenv("DEVSY_CLIENT_PERMISSION_MASK"), 10, 9)
	require.NoError(t, err)
	f := cachePermissionFixture{
		root: os.Getenv(
			"DEVSY_CLIENT_PERMISSION_ROOT",
		),
		mode:     mode,
		platform: CacheFolder,
		mask:     os.FileMode(mask),
	}
	// This test is selected alone in an isolated process. Never change the parent's umask.
	syscall.Umask(int(f.mask))
	require.Equal(t, os.Getenv("DEVSY_CLIENT_CACHE_EXPECTED"), CacheFolder)
	f.assertFresh(t)
	path := f.configPath()
	if mode == cachePermissionBlocked || mode == cachePermissionUnwritable {
		c := &client{config: NewConfig(), configPath: path}
		require.Error(t, c.Save(), "explicit Save must surface an unusable default root")
		require.Equal(t, path, c.configPath)
		return
	}
	f.saveAndReload(t, path)
	f.assertSavedModes(t, path)
}

func (f cachePermissionFixture) configPath() string {
	switch f.mode {
	case cachePermissionCustom:
		return filepath.Join(f.root, "custom", "config.json")
	case cachePermissionPrefix:
		return filepath.Join(filepath.Dir(f.platform)+"-custom", "config.json")
	case cachePermissionNested:
		return filepath.Join(f.platform, "other.json")
	default:
		return DefaultCacheConfig
	}
}

func (f cachePermissionFixture) assertFresh(t *testing.T) {
	t.Helper()
	switch f.mode {
	case cachePermissionExisting,
		cachePermissionOverrideExisting,
		cachePermissionBlocked,
		cachePermissionUnwritable:
		return
	}
	require.NoFileExists(t, DefaultCacheConfig)
	require.NoDirExists(t, f.platform, "import must leave fresh credential parents absent")
	if f.mode != cachePermissionUnresolved {
		require.NoDirExists(
			t,
			filepath.Dir(f.platform),
			"import must leave the category root absent",
		)
	}
}

func (f cachePermissionFixture) saveAndReload(t *testing.T, path string) {
	t.Helper()
	before, err := dependencytest.Tree(f.root)
	require.NoError(t, err)
	c, err := NewClientFromPath(path)
	require.NoError(t, err)
	afterRead, err := dependencytest.Tree(f.root)
	require.NoError(t, err)
	require.Equal(t, before, afterRead, "reading a missing config must remain passive")
	// Fixture-only local persistence: no real authentication, browser or network.
	require.NoError(t, c.LoginRaw("https://fixture.invalid", "fixture-key", false))
	reloaded, err := NewClientFromPath(path)
	require.NoError(t, err)
	require.Equal(t, c.Config(), reloaded.Config())
	require.NoError(t, reloaded.Save())
}

func (f cachePermissionFixture) assertSavedModes(t *testing.T, path string) {
	t.Helper()
	wantFileMode := 0o660 &^ f.mask
	category := filepath.Dir(f.platform)
	switch f.mode {
	case cachePermissionExisting, cachePermissionOverrideExisting:
		wantFileMode = 0o600
		assertCachePermissionMode(t, category, 0o751)
		assertCachePermissionMode(t, f.platform, 0o750)
	case cachePermissionNative, cachePermissionOverride:
		assertCachePermissionMode(t, category, 0o700)
		assertCachePermissionMode(t, f.platform, 0o755&^f.mask)
	case cachePermissionNested:
		assertCachePermissionMode(t, category, 0o755&^f.mask)
		assertCachePermissionMode(t, filepath.Dir(path), 0o755&^f.mask)
	case cachePermissionCustom, cachePermissionPrefix:
		require.NoDirExists(t, category, "custom Save must not create the default category")
		assertCachePermissionMode(t, filepath.Dir(path), 0o755&^f.mask)
	case cachePermissionUnresolved:
		assertCachePermissionMode(t, filepath.Dir(path), 0o755&^f.mask)
	}
	assertCachePermissionMode(t, path, wantFileMode)
	assertCachePermissionMode(t, f.root, 0o755)
	if f.mode != cachePermissionUnresolved {
		assertCachePermissionMode(t, filepath.Dir(category), 0o755)
	}
}

func assertCachePermissionMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, want, info.Mode().Perm(), "%s", path)
}
