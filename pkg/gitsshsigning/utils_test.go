package gitsshsigning

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractGitConfiguration_WorkingDirResolvesIncludeIf(t *testing.T) {
	tmpHome := tempDirResolved(t)
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	projectDir := tempDirResolved(t)
	//nolint:gosec // test-only, args are constants
	require.NoError(t, exec.Command("git", "init", projectDir).Run())

	projectConfigPath := filepath.Join(tmpHome, "project.gitconfig")
	require.NoError(t, os.WriteFile(projectConfigPath, []byte(`[gpg]
	format = ssh
[user]
	signingkey = /path/to/signing.pub
`), 0o600))

	globalConfigPath := filepath.Join(tmpHome, ".gitconfig")
	require.NoError(t, os.WriteFile(globalConfigPath, fmt.Appendf(nil, `[includeIf "gitdir:%s/"]
	path = %s
`, projectDir, projectConfigPath), 0o600))

	format, signingKey, err := ExtractGitConfiguration(context.Background(), projectDir)
	require.NoError(t, err)
	assert.Equal(t, GPGFormatSSH, format)
	assert.Equal(t, "/path/to/signing.pub", signingKey)
}

func TestExtractGitConfiguration_WorkingDirWithGlobalSigningConfig(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	projectDir := t.TempDir()
	//nolint:gosec // test-only, args are constants
	require.NoError(t, exec.Command("git", "init", projectDir).Run())

	globalConfigPath := filepath.Join(tmpHome, ".gitconfig")
	require.NoError(t, os.WriteFile(globalConfigPath, []byte(`[gpg]
	format = ssh
[user]
	signingkey = /global/key.pub
`), 0o600))

	format, signingKey, err := ExtractGitConfiguration(context.Background(), projectDir)
	require.NoError(t, err)
	assert.Equal(t, GPGFormatSSH, format)
	assert.Equal(t, "/global/key.pub", signingKey)
}

// tempDirResolved returns a t.TempDir() with symlinks resolved. On macOS the
// temp dir lives under /var (a symlink to /private/var); git's `includeIf
// gitdir:` matches against the resolved path, so tests that build such patterns
// must use the resolved form or they silently fail to match.
func tempDirResolved(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return dir
}
