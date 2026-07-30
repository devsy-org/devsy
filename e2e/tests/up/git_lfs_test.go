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

func TestCloneLFSRepoSucceedsWithoutGitLFSBinary(t *testing.T) {
	const gitBin = "git"

	if !command.Exists(gitBin) {
		t.Skip("git not installed")
	}

	realGit, err := exec.LookPath(gitBin)
	if err != nil {
		t.Skip("git not found on PATH")
	}

	binDir := t.TempDir()
	if err := os.Symlink(realGit, filepath.Join(binDir, gitBin)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	runGit(t, home, "config", "--global", "user.email", "test@example.com")
	runGit(t, home, "config", "--global", "user.name", "Test")
	runGit(t, home, "config", "--global", "filter.lfs.process", "git-lfs filter-process")
	runGit(t, home, "config", "--global", "filter.lfs.smudge", "git-lfs smudge -- %f")
	runGit(t, home, "config", "--global", "filter.lfs.clean", "git-lfs clean -- %f")

	sourceDir := t.TempDir()
	runGit(t, sourceDir, "init", "--quiet")
	runGit(t, sourceDir, "config", "filter.lfs.clean", "cat")
	runGit(t, sourceDir, "config", "filter.lfs.smudge", "cat")
	runGit(t, sourceDir, "config", "filter.lfs.process", "")

	pointer := "version https://git-lfs.github.com/spec/v1\n" +
		"oid sha256:0000000000000000000000000000000000000000000000000000000000000\n" +
		"size 4\n"
	if err := os.WriteFile(
		filepath.Join(sourceDir, ".gitattributes"),
		[]byte("*.bin filter=lfs diff=lfs merge=lfs -text\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "data.bin"), []byte(pointer), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, sourceDir, "add", ".")
	runGit(t, sourceDir, "commit", "--quiet", "-m", "add lfs-tracked file")

	targetDir := filepath.Join(t.TempDir(), "clone")
	if err := git.At(targetDir).Clone(context.Background(), sourceDir); err != nil {
		t.Fatalf("clone: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(targetDir, "data.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != pointer {
		t.Errorf("data.bin content = %q, want pointer stub %q", got, pointer)
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
