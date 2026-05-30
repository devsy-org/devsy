package tunnel

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/assert"
)

const testCtxName = "default"

// newConfigWithExitAfterTimeout returns a *config.Config whose default
// context sets EXIT_AFTER_TIMEOUT to the given value.
func newConfigWithExitAfterTimeout(value string) *config.Config {
	return &config.Config{
		DefaultContext: testCtxName,
		Contexts: map[string]*config.ContextConfig{
			testCtxName: {
				Options: map[string]config.OptionValue{
					config.ContextOptionExitAfterTimeout: {Value: value},
				},
			},
		},
	}
}

func TestGetExitAfterTimeout_EnabledByContextOption(t *testing.T) {
	cfg := newConfigWithExitAfterTimeout(config.BoolTrue)
	assert.Equal(t, defaultExitTimeout, getExitAfterTimeout(cfg, false))
}

func TestGetExitAfterTimeout_DisabledByContextOption(t *testing.T) {
	cfg := newConfigWithExitAfterTimeout(config.BoolFalse)
	assert.Equal(t, time.Duration(0), getExitAfterTimeout(cfg, false))
}

func TestGetExitAfterTimeout_DisableIdleTimeoutOverridesEnabled(t *testing.T) {
	cfg := newConfigWithExitAfterTimeout(config.BoolTrue)
	assert.Equal(
		t,
		time.Duration(0),
		getExitAfterTimeout(cfg, true),
	)
}

func TestGetExitAfterTimeout_DisableIdleTimeoutOverridesDisabled(t *testing.T) {
	cfg := newConfigWithExitAfterTimeout(config.BoolFalse)
	assert.Equal(
		t,
		time.Duration(0),
		getExitAfterTimeout(cfg, true),
	)
}

const testBaseCommand = "devsy internal agent container credentials-server --user root"

func TestAddGitSSHSigningKey_ExplicitKey(t *testing.T) {
	command := testBaseCommand
	result := addGitSSHSigningKey(command, "/path/to/key.pub", "")

	encoded := base64.StdEncoding.EncodeToString([]byte("/path/to/key.pub"))
	assert.Equal(t, command+" --git-user-signing-key "+encoded, result)
}

func TestAddGitSSHSigningKey_ExplicitKeyTakesPrecedence(t *testing.T) {
	// When an explicit key is provided, it should be used regardless
	// of what ExtractGitConfiguration might return from host .gitconfig.
	command := testBaseCommand
	explicitKey := "/explicit/key.pub"
	result := addGitSSHSigningKey(command, explicitKey, "")

	encoded := base64.StdEncoding.EncodeToString([]byte(explicitKey))
	assert.Equal(t, command+" --git-user-signing-key "+encoded, result)
}

func TestAddGitSSHSigningKey_EmptyExplicitKey_FallsBackToHostConfig(t *testing.T) {
	// Ensure deterministic environment with no host git signing config.
	command := testBaseCommand
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", tmpHome)

	result := addGitSSHSigningKey(command, "", "")

	assert.Equal(t, command, result)
	assert.NotContains(t, result, "--git-user-signing-key")
}

func TestBuildCredentialsCommand_IncludesSigningKey(t *testing.T) {
	opts := RunServicesOptions{
		User:                           "testuser",
		ConfigureGitSSHSignatureHelper: true,
		GitSSHSigningKey:               "/my/key.pub",
	}
	command := buildCredentialsCommand(opts)

	encoded := base64.StdEncoding.EncodeToString([]byte("/my/key.pub"))
	assert.Contains(t, command, "--git-user-signing-key "+encoded)
	assert.Contains(t, command, "--user testuser")
}

func TestBuildCredentialsCommand_NoSigningKey(t *testing.T) {
	opts := RunServicesOptions{
		User:                           "testuser",
		ConfigureGitSSHSignatureHelper: false,
	}
	command := buildCredentialsCommand(opts)

	assert.NotContains(t, command, "--git-user-signing-key")
}
