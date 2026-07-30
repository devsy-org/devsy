package up

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/git"
)

const lfsPointer = "version https://git-lfs.github.com/spec/v1\n" +
	"oid sha256:0000000000000000000000000000000000000000000000000000000000000\n" +
	"size 4\n"

func TestCloneLFSRepoSucceedsWithoutGitLFSBinary(t *testing.T) {
	hideGitLFSFromPath(t)
	simulateStaleGlobalLFSConfig(t)

	sourceDir := newLFSFixtureRepo(t)

	targetDir := filepath.Join(t.TempDir(), "clone")
	if err := git.At(targetDir).Clone(context.Background(), sourceDir); err != nil {
		t.Fatalf("clone: %v", err)
	}

	dataPath := filepath.Join(targetDir, "data.bin")
	got, err := os.ReadFile(dataPath) // #nosec G304 -- path is under a test-owned t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != lfsPointer {
		t.Errorf("data.bin content = %q, want pointer stub %q", got, lfsPointer)
	}
}

// hideGitLFSFromPath restricts PATH to a directory containing only "git",
// simulating a host without the git-lfs binary installed.
func hideGitLFSFromPath(t *testing.T) {
	t.Helper()

	if !command.Exists("git") {
		t.Skip("git not installed")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found on PATH")
	}

	binDir := t.TempDir()
	if err := os.Symlink(realGit, filepath.Join(binDir, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
}

// simulateStaleGlobalLFSConfig sets a fresh, isolated global gitconfig that
// registers the LFS filter driver, as if `git lfs install --global` had been
// run in the past even though the git-lfs binary is no longer present.
func simulateStaleGlobalLFSConfig(t *testing.T) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	runGit(t, home, "config", "--global", "user.email", "test@example.com")
	runGit(t, home, "config", "--global", "user.name", "Test")
	runGit(t, home, "config", "--global", "filter.lfs.process", "git-lfs filter-process")
	runGit(t, home, "config", "--global", "filter.lfs.smudge", "git-lfs smudge -- %f")
	runGit(t, home, "config", "--global", "filter.lfs.clean", "git-lfs clean -- %f")
}

// newLFSFixtureRepo creates a repo declaring an LFS-tracked file, using local
// filter overrides so fixture setup doesn't need the (absent) git-lfs binary.
func newLFSFixtureRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	runGit(t, dir, "init", "--quiet")
	runGit(t, dir, "config", "filter.lfs.clean", "cat")
	runGit(t, dir, "config", "filter.lfs.smudge", "cat")
	runGit(t, dir, "config", "filter.lfs.process", "")

	writeFile(
		t,
		filepath.Join(dir, ".gitattributes"),
		"*.bin filter=lfs diff=lfs merge=lfs -text\n",
	)
	writeFile(t, filepath.Join(dir, "data.bin"), lfsPointer)

	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "--quiet", "-m", "add lfs-tracked file")
	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) // #nosec G204 -- fixed binary name, test-only
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
