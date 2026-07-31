package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/assert"
)

func TestPkgManagerStrategyAptArgs(t *testing.T) {
	fake := &fakeRunner{}
	s := &pkgManagerStrategy{
		manager: strategyApt,
		runner:  fake,
		installArgs: func(pkg string) [][]string {
			return [][]string{{pkgUpdate}, {"-y", "install", pkg}}
		},
	}

	assert.NilError(t, s.install(context.Background(), lfsTool))
	assert.Equal(t, 2, len(fake.calls))
	assert.Equal(t, "apt", fake.calls[0].Binary)
	assert.DeepEqual(t, []string{pkgUpdate}, fake.calls[0].Args)
	assert.DeepEqual(t, []string{"-y", "install", "git-lfs"}, fake.calls[1].Args)
}

func TestPkgManagerStrategyStopsOnError(t *testing.T) {
	fake := &fakeRunner{err: fmt.Errorf("boom")}
	s := &pkgManagerStrategy{
		manager: strategyApk,
		runner:  fake,
		installArgs: func(pkg string) [][]string {
			return [][]string{{pkgUpdate}, {"add", pkg}}
		},
	}

	err := s.install(context.Background(), lfsTool)
	assert.Assert(t, err != nil)
	// The second command must not run after the first fails.
	assert.Equal(t, 1, len(fake.calls))
}

func TestReleaseStrategyRequiresReleaseSource(t *testing.T) {
	// git has no release source; the release strategy must refuse it.
	err := releaseStrategy{}.install(context.Background(), gitTool)
	assert.Assert(t, err != nil)
}

func TestPkgManagerStrategyUsableRequiresRoot(t *testing.T) {
	s := &pkgManagerStrategy{manager: "sh"} // "sh" always exists in test environments

	original := isRoot
	t.Cleanup(func() { isRoot = original })

	isRoot = func() bool { return true }
	assert.Assert(t, s.usable())

	isRoot = func() bool { return false }
	assert.Assert(t, !s.usable())
}

func TestEnsureDirWritable(t *testing.T) {
	assert.NilError(t, ensureDirWritable(t.TempDir()))
}

func TestEnsureDirWritableCreatesMissingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does", "not", "exist", "yet")
	assert.NilError(t, ensureDirWritable(dir))
}

func TestEnsureDirWritableRejectsReadOnlyDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission test not meaningful when running as root")
	}

	dir := t.TempDir()
	// #nosec G302 -- intentional: testing restrictive perms
	assert.NilError(t, os.Chmod(dir, 0o500))

	assert.Assert(t, ensureDirWritable(dir) != nil)
}

// fakeStrategy is a test installStrategy with configurable behavior.
type fakeStrategy struct {
	label     string
	isUsable  bool
	installer func() error
	installed *bool // set true by install to simulate the binary appearing
}

func (f *fakeStrategy) name() string { return f.label }
func (f *fakeStrategy) usable() bool { return f.isUsable }
func (f *fakeStrategy) install(context.Context, tool) error {
	err := f.installer()
	if err == nil && f.installed != nil {
		*f.installed = true
	}
	return err
}

func TestEnsureFallsThroughOnInstallFailure(t *testing.T) {
	// A usable strategy that fails must not abort the sequence; a later
	// strategy should still get a chance and can succeed.
	firstTried, secondTried := false, false
	installed := false

	inst := &Installer{strategies: []installStrategy{
		&fakeStrategy{
			label:    strategyApt,
			isUsable: true,
			installer: func() error {
				firstTried = true
				return fmt.Errorf("no root")
			},
		},
		&fakeStrategy{
			label:     strategyRelease,
			isUsable:  true,
			installed: &installed,
			installer: func() error {
				secondTried = true
				return nil
			},
		},
	}}

	// command.Exists reports the real git-lfs; guard the assertion on the
	// simulated flag instead, which the second strategy sets.
	_ = inst.ensure(context.Background(), tool{binary: "definitely-not-a-real-binary-xyz"})
	assert.Assert(t, firstTried)
	assert.Assert(t, secondTried)
	assert.Assert(t, installed)
}

func TestEnsureAggregatesErrorsWhenAllFail(t *testing.T) {
	inst := &Installer{strategies: []installStrategy{
		&fakeStrategy{label: strategyApt, isUsable: true, installer: func() error {
			return fmt.Errorf("apt boom")
		}},
		&fakeStrategy{label: strategyRelease, isUsable: true, installer: func() error {
			return fmt.Errorf("release boom")
		}},
	}}

	err := inst.ensure(context.Background(), tool{binary: "definitely-not-a-real-binary-xyz"})
	assert.Assert(t, err != nil)
	// Both strategy failures must surface, not just the first.
	assert.Assert(t, strings.Contains(err.Error(), "apt boom"))
	assert.Assert(t, strings.Contains(err.Error(), "release boom"))
}

func TestGitLFSReleaseAsset(t *testing.T) {
	// linux uses .tar.gz; darwin and windows publish .zip.
	cases := map[string]string{
		"linux/amd64":   "git-lfs-linux-amd64-v3.5.1.tar.gz",
		"darwin/arm64":  "git-lfs-darwin-arm64-v3.5.1.zip",
		"windows/amd64": "git-lfs-windows-amd64-v3.5.1.zip",
	}
	for platform, want := range cases {
		goos, goarch, _ := strings.Cut(platform, "/")
		asset, err := gitLFSRelease.assetName(goos, goarch, "3.5.1")
		assert.NilError(t, err)
		assert.Equal(t, want, asset)
		assert.Equal(
			t,
			"https://github.com/git-lfs/git-lfs/releases/download/v3.5.1/"+want,
			gitLFSRelease.downloadURL("3.5.1", asset),
		)
	}
}

func TestGitLFSReleaseAssetUnsupportedPlatform(t *testing.T) {
	_, err := gitLFSRelease.assetName("plan9", "amd64", "3.5.1")
	assert.Assert(t, err != nil)

	_, err = gitLFSRelease.assetName("linux", "mips", "3.5.1")
	assert.Assert(t, err != nil)
}

func TestGitLFSReleaseAllAssetsHaveChecksums(t *testing.T) {
	// Every asset assetName can resolve must have a pinned checksum, or install
	// fails closed at runtime.
	for _, goos := range []string{osLinux, osDarwin, osWindows} {
		for _, goarch := range []string{"amd64", "arm64"} {
			asset, err := gitLFSRelease.assetName(goos, goarch, gitLFSRelease.version)
			assert.NilError(t, err)
			_, ok := gitLFSRelease.checksums[asset]
			assert.Assert(t, ok, "missing checksum for %s", asset)
		}
	}
}

func TestExecutableName(t *testing.T) {
	assert.Equal(t, "git-lfs", executableName("git-lfs", osLinux))
	assert.Equal(t, "git-lfs", executableName("git-lfs", osDarwin))
	assert.Equal(t, "git-lfs.exe", executableName("git-lfs", osWindows))
}
