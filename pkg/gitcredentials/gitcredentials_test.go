package gitcredentials

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetUser_WorkingDirResolvesIncludeIf(t *testing.T) {
	tmpHome := tempDirResolved(t)
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	projectDir := tempDirResolved(t)
	//nolint:gosec // test-only, args are constants
	require.NoError(t, exec.Command("git", "init", projectDir).Run())

	projectConfigPath := filepath.Join(tmpHome, "project.gitconfig")
	require.NoError(t, os.WriteFile(projectConfigPath, []byte(`[user]
	name = Project User
	email = project@example.com
`), 0o600))

	globalConfigPath := filepath.Join(tmpHome, ".gitconfig")
	require.NoError(t, os.WriteFile(globalConfigPath, fmt.Appendf(nil, `[user]
	name = Global User
	email = global@example.com
[includeIf "gitdir:%s/"]
	path = %s
`, projectDir, projectConfigPath), 0o600))

	user, err := GetUser(context.Background(), "", projectDir)
	require.NoError(t, err)
	assert.Equal(t, "Project User", user.Name)
	assert.Equal(t, "project@example.com", user.Email)
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

func TestGetUser_EmptyWorkingDirUsesGlobal(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	globalConfigPath := filepath.Join(tmpHome, ".gitconfig")
	require.NoError(t, os.WriteFile(globalConfigPath, []byte(`[user]
	name = Global User
	email = global@example.com
`), 0o600))

	user, err := GetUser(context.Background(), "", "")
	require.NoError(t, err)
	assert.Equal(t, "Global User", user.Name)
	assert.Equal(t, "global@example.com", user.Email)
}

func TestGetUser_WorkingDirWithNoMatchingIncludeIfUsesGlobal(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	otherDir := t.TempDir()

	projectConfigPath := filepath.Join(tmpHome, "project.gitconfig")
	require.NoError(t, os.WriteFile(projectConfigPath, []byte(`[user]
	name = Project User
	email = project@example.com
`), 0o600))

	globalConfigPath := filepath.Join(tmpHome, ".gitconfig")
	require.NoError(t, os.WriteFile(globalConfigPath, fmt.Appendf(nil, `[user]
	name = Global User
	email = global@example.com
[includeIf "gitdir:%s/"]
	path = %s
`, otherDir, projectConfigPath), 0o600))

	projectDir := t.TempDir()
	//nolint:gosec // test-only, args are constants
	require.NoError(t, exec.Command("git", "init", projectDir).Run())

	user, err := GetUser(context.Background(), "", projectDir)
	require.NoError(t, err)
	assert.Equal(t, "Global User", user.Name)
	assert.Equal(t, "global@example.com", user.Email)
}

func TestCredentialsCodecRoundTrip(t *testing.T) {
	original := GitCredentials{
		Protocol: "https",
		Host:     "github.com",
		Path:     "org/repo.git",
		Username: "user",
		Password: "tok=en", // value containing '=' must survive
	}

	decoded := ParseCredentials(original.Encode())
	assert.Equal(t, original, decoded)
}

func TestEncodeOmitsEmptyFields(t *testing.T) {
	encoded := GitCredentials{Protocol: "https", Host: "github.com"}.Encode()
	assert.Equal(t, "protocol=https\nhost=github.com\n", encoded)
}

func TestParseCredentialsIgnoresUnknownAndBlankLines(t *testing.T) {
	creds := ParseCredentials("protocol=https\n\ngarbage\nquit=1\nhost=example.com\n")
	assert.Equal(t, "https", creds.Protocol)
	assert.Equal(t, "example.com", creds.Host)
}

func TestSetAndGetUserRoundTrip(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	ctx := context.Background()
	name := `Injector"; touch /tmp/pwned #`
	require.NoError(t, SetUser(ctx, "", &GitUser{Name: name, Email: "x@example.com"}))

	user, err := GetUser(ctx, "", "")
	require.NoError(t, err)
	assert.Equal(t, name, user.Name)
	assert.Equal(t, "x@example.com", user.Email)
}

func TestSetUserEmptyIdentityIsNoOp(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	// A named user with an empty identity must not attempt to chown a config
	// file that was never written (regression: unconditional chown failed with
	// ENOENT, aborting container credential/signing setup).
	require.NoError(t, SetUser(context.Background(), "", &GitUser{}))
	assert.NoFileExists(t, filepath.Join(tmpHome, ".gitconfig"))
}

// helperTestEnv points git's global config at a temp HOME so ConfigureHelper /
// RemoveHelper operate on an isolated file. It returns the config path.
func helperTestEnv(t *testing.T) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	return filepath.Join(tmpHome, ".gitconfig")
}

func gitConfigGetAll(t *testing.T, path, key string) []string {
	t.Helper()
	// #nosec G204 -- test-only, path/key are test-controlled
	out, err := exec.Command("git", "config", "--file", path, "--get-all", key).Output()
	if err != nil {
		return nil
	}
	var vals []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			vals = append(vals, line)
		}
	}
	return vals
}

func TestConfigureHelperInstallsAndIsIdempotent(t *testing.T) {
	path := helperTestEnv(t)

	require.NoError(t, ConfigureHelper(context.Background(), "/usr/local/bin/devsy", "", 8022))
	want := `!'/usr/local/bin/devsy' internal agent git-credentials --port 8022`
	assert.Equal(t, []string{want}, gitConfigGetAll(t, path, "credential.helper"))

	require.NoError(t, ConfigureHelper(context.Background(), "/usr/local/bin/devsy", "", 8022))
	assert.Equal(t, []string{want}, gitConfigGetAll(t, path, "credential.helper"))
}

func TestConfigureHelperReplacesExisting(t *testing.T) {
	path := helperTestEnv(t)
	// #nosec G204 -- test-only, path is test-controlled
	cmd := exec.Command("git", "config", "--file", path, "--add", "credential.helper", "store")
	require.NoError(t, cmd.Run())

	require.NoError(t, ConfigureHelper(context.Background(), "/usr/local/bin/devsy", "", -1))
	want := `!'/usr/local/bin/devsy' internal agent git-credentials`
	assert.Equal(t, []string{want}, gitConfigGetAll(t, path, "credential.helper"))
}

func TestRemoveHelperPreservesOtherCredentialKeys(t *testing.T) {
	path := helperTestEnv(t)

	require.NoError(t, ConfigureHelper(context.Background(), "/usr/local/bin/devsy", "", 1))
	// #nosec G204 -- test-only, path is test-controlled
	cmd := exec.Command("git", "config", "--file", path, "credential.useHttpPath", "true")
	require.NoError(t, cmd.Run())

	require.NoError(t, RemoveHelperFromPath(context.Background(), path))

	assert.Empty(t, gitConfigGetAll(t, path, "credential.helper"))
	assert.Equal(t, []string{"true"}, gitConfigGetAll(t, path, "credential.useHttpPath"))
}

func TestRemoveHelperOnMissingConfigIsNoError(t *testing.T) {
	require.NoError(
		t,
		RemoveHelperFromPath(context.Background(), filepath.Join(t.TempDir(), "does-not-exist")),
	)
}
