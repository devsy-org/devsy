package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(
		os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

func commitFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "change "+name)
	return runGit(t, dir, "rev-parse", "HEAD")
}

// The workflow checks out one commit, so the classifier must work on a shallow
// clone that only fetches the previous main commit.
func TestClassifyPushOnShallowCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	manifest, err := LoadManifest()
	require.NoError(t, err)

	tests := []struct {
		name  string
		file  string
		light bool
	}{
		{"docs change", "sites/docs-devsy-sh/content/docs/index.mdx", true},
		{"go change", "pkg/shell/shell.go", false},
		{"install script", "sites/docs-devsy-sh/public/install.sh", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origin := t.TempDir()
			runGit(t, origin, "init", "-q", "-b", "main")
			before := commitFile(t, origin, "README.md", "one")
			sha := commitFile(t, origin, tt.file, "two")
			clone := filepath.Join(t.TempDir(), "clone")
			runGit(t, origin, "clone", "-q", "--no-local", "--depth=1", origin, clone)

			decision := classifyPush(
				context.Background(),
				gitHistory{runner: execRunner{}, dir: clone},
				manifest.Selection,
				pushRange{before, sha},
			)

			require.Equal(t, tt.light, decision.Light, decision.Reason)
		})
	}
}
