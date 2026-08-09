package ssh

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeUserConfig writes an SSH config containing a Devsy-managed host section
// for the given workspace ID with the provided User directive (or none when
// user is empty). The section uses the real marker format scanHostSection reads.
func writeUserConfig(t *testing.T, path, workspaceID, user string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))

	host := workspaceID + config.SSHHostSuffix
	section := MarkerStartPrefix + host + "\nHost " + host + "\n"
	if user != "" {
		section += "  User " + user + "\n"
	}
	section += MarkerEndPrefix + host + "\n"

	require.NoError(t, os.WriteFile(path, []byte(section), 0o600))
}

// TestGetUser_ReturnsRootWhenConfigMissing verifies the default "root" user is
// returned when no SSH config file exists for the workspace.
func TestGetUser_ReturnsRootWhenConfigMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	user, err := GetUser("myws", "", "")
	require.NoError(t, err)
	assert.Equal(t, "root", user)
}

// TestGetUser_ReturnsConfiguredUser verifies the User directive from the
// workspace's host section is returned.
func TestGetUser_ReturnsConfiguredUser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".ssh", "config")
	writeUserConfig(t, configPath, "myws", "vscode")

	user, err := GetUser("myws", configPath, "")
	require.NoError(t, err)
	assert.Equal(t, "vscode", user)
}

// TestGetUser_ReturnsRootWhenNoUserDirective verifies the default is returned
// when the host section exists but lacks a User line.
func TestGetUser_ReturnsRootWhenNoUserDirective(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".ssh", "config")
	writeUserConfig(t, configPath, "myws", "")

	user, err := GetUser("myws", configPath, "")
	require.NoError(t, err)
	assert.Equal(t, "root", user)
}

// TestGetUser_ReturnsRootWhenHostSectionAbsent verifies the default is returned
// when the config file exists but contains no section for the workspace.
func TestGetUser_ReturnsRootWhenHostSectionAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".ssh", "config")
	// Config has a section for a different workspace only.
	writeUserConfig(t, configPath, "otherws", "vscode")

	user, err := GetUser("myws", configPath, "")
	require.NoError(t, err)
	assert.Equal(t, "root", user)
}

// TestGetUser_IncludePathTakesPrecedence verifies that when an include path is
// supplied, the user is read from it rather than the primary config path. This
// mirrors how ConfigureSSHConfig routes writes through the include path.
func TestGetUser_IncludePathTakesPrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	configPath := filepath.Join(home, ".ssh", "config")
	includePath := filepath.Join(home, ".ssh", "devsy_config")

	writeUserConfig(t, configPath, "myws", "from-main")
	writeUserConfig(t, includePath, "myws", "from-include")

	user, err := GetUser("myws", configPath, includePath)
	require.NoError(t, err)
	assert.Equal(t, "from-include", user)
}

// TestGetUser_ResolvesEmptyPathToDefault verifies an empty config path resolves
// to ~/.ssh/config, the same default ConfigureSSHConfig writes to.
func TestGetUser_ResolvesEmptyPathToDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".ssh", "config")
	writeUserConfig(t, configPath, "myws", "vscode")

	user, err := GetUser("myws", "", "")
	require.NoError(t, err)
	assert.Equal(t, "vscode", user)
}

// TestGetUser_UnquotesQuotedUser verifies a quoted User value (without inner
// spaces) is unquoted, as written by SSH clients that quote values containing
// special characters.
func TestGetUser_UnquotesQuotedUser(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".ssh", "config")

	host := "myws" + config.SSHHostSuffix
	section := MarkerStartPrefix + host + "\nHost " + host + "\n" +
		"  User \"custom-user\"\n" + MarkerEndPrefix + host + "\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(configPath), 0o700))
	require.NoError(t, os.WriteFile(configPath, []byte(section), 0o600))

	user, err := GetUser("myws", configPath, "")
	require.NoError(t, err)
	assert.Equal(t, "custom-user", user)
}
