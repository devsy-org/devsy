package gitsshsigning

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSyntheticPublicKey   = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest test@example.com"
	testKeyPrefixedPublicKey = "key::ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAITest"
	testSSHDirName           = ".ssh"
	testSigningKeyName       = "git_signing_ed25519"
	testGPGKeyID             = "ABCDEF1234567890"
)

func TestExtractGitConfiguration_WorkingDirResolvesIncludeIf(t *testing.T) {
	tmpHome := tempDirResolved(t)
	setupIsolatedGitEnv(t, tmpHome)

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
	setupIsolatedGitEnv(t, tmpHome)

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

func TestIsLiteralSSHKey(t *testing.T) {
	assert.True(t, IsLiteralSSHKey(testSyntheticPublicKey))
	assert.True(t, IsLiteralSSHKey(testKeyPrefixedPublicKey))
	assert.True(t, IsLiteralSSHKey("key::ecdsa-sha2-nistp256 AAAAE2VjZHNh"))
	assert.False(t, IsLiteralSSHKey(testTildeKeyPath))
	assert.False(t, IsLiteralSSHKey("/Users/test/.ssh/git_signing_ed25519"))
	assert.False(t, IsLiteralSSHKey(testGPGKeyID))
	assert.False(t, IsLiteralSSHKey(""))
}

func TestExpandSigningKeyPath(t *testing.T) {
	tmpHome := t.TempDir()
	setTestHome(t, tmpHome)

	assert.Equal(t, "", ExpandSigningKeyPath(""))
	assert.Equal(t, tmpHome, ExpandSigningKeyPath("~"))
	assert.Equal(
		t,
		filepath.Join(tmpHome, testSSHDirName, testSigningKeyName),
		ExpandSigningKeyPath(testTildeKeyPath),
	)
	assert.Equal(
		t,
		filepath.Join(tmpHome, `.ssh\git_signing_ed25519`),
		ExpandSigningKeyPath(`~\.ssh\git_signing_ed25519`),
	)
	assert.Equal(t, testSyntheticPublicKey, ExpandSigningKeyPath(testSyntheticPublicKey))
	assert.Equal(t, testKeyPrefixedPublicKey, ExpandSigningKeyPath(testKeyPrefixedPublicKey))

	unknownUser := "~nonexistent_devsy_user_1723/.ssh/id_ed25519"
	assert.Equal(t, unknownUser, ExpandSigningKeyPath(unknownUser))

	currentUser, err := user.Current()
	if err == nil && currentUser.Username != "" && currentUser.HomeDir != "" {
		assert.Equal(
			t,
			currentUser.HomeDir,
			ExpandSigningKeyPath("~"+currentUser.Username),
		)
		assert.Equal(
			t,
			filepath.Join(currentUser.HomeDir, testSSHDirName, "id_ed25519"),
			ExpandSigningKeyPath("~"+currentUser.Username+"/.ssh/id_ed25519"),
		)
	}
}

func TestExtractGitConfiguration_ResolvesSSHKeyFormats(t *testing.T) {
	tmpHome := t.TempDir()
	setupIsolatedGitEnv(t, tmpHome)

	projectDir := t.TempDir()
	//nolint:gosec // test-only, args are constants
	require.NoError(t, exec.Command("git", "init", projectDir).Run())

	cases := []struct {
		name, format, rawKey, expectedKey string
	}{
		{
			"tilde ssh",
			GPGFormatSSH,
			testTildeKeyPath,
			filepath.Join(tmpHome, testSSHDirName, testSigningKeyName),
		},
		{"literal ssh-", GPGFormatSSH, testSyntheticPublicKey, testSyntheticPublicKey},
		{"literal key::", GPGFormatSSH, testKeyPrefixedPublicKey, testKeyPrefixedPublicKey},
		{
			"relative ssh",
			GPGFormatSSH,
			".ssh/repo_key",
			filepath.Join(projectDir, testSSHDirName, "repo_key"),
		},
		{"non-ssh gpg id", "openpgp", testGPGKeyID, testGPGKeyID},
	}
	globalConfigPath := filepath.Join(tmpHome, ".gitconfig")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := fmt.Sprintf(
				"[gpg]\n\tformat = %s\n[user]\n\tsigningkey = %s\n",
				tc.format,
				tc.rawKey,
			)
			require.NoError(t, os.WriteFile(globalConfigPath, []byte(cfg), 0o600))

			gotFormat, gotKey, err := ExtractGitConfiguration(context.Background(), projectDir)
			require.NoError(t, err)
			assert.Equal(t, tc.format, gotFormat)
			assert.Equal(t, tc.expectedKey, gotKey)
		})
	}
}

func TestExtractGitConfiguration_ExpandsNamedUserForSSH(t *testing.T) {
	currentUser, err := user.Current()
	if err != nil || currentUser.Username == "" || currentUser.HomeDir == "" {
		t.Skip("current OS user unavailable")
	}

	tmpHome := t.TempDir()
	setupIsolatedGitEnv(t, tmpHome)

	projectDir := t.TempDir()
	//nolint:gosec // test-only, args are constants
	require.NoError(t, exec.Command("git", "init", projectDir).Run())

	globalConfigPath := filepath.Join(tmpHome, ".gitconfig")
	cfg := fmt.Sprintf(
		"[gpg]\n\tformat = ssh\n[user]\n\tsigningkey = ~%s/.ssh/git_signing_ed25519\n",
		currentUser.Username,
	)
	require.NoError(t, os.WriteFile(globalConfigPath, []byte(cfg), 0o600))

	gotFormat, gotKey, err := ExtractGitConfiguration(context.Background(), projectDir)
	require.NoError(t, err)
	assert.Equal(t, GPGFormatSSH, gotFormat)
	assert.Equal(
		t,
		filepath.Join(currentUser.HomeDir, testSSHDirName, testSigningKeyName),
		gotKey,
	)
}

func setTestHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func setupIsolatedGitEnv(t *testing.T, home string) {
	t.Helper()
	setTestHome(t, home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
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
