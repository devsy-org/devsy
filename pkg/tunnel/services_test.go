package tunnel

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/stretchr/testify/assert"
)

const testCtxName = "default"

const testServiceOwner = "fixture-owner"

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
	result := addGitSSHSigningKey(context.Background(), command, "/path/to/key.pub", "")

	encoded := base64.StdEncoding.EncodeToString([]byte("/path/to/key.pub"))
	assert.Equal(t, command+" --git-user-signing-key "+encoded, result)
}

func TestAddGitSSHSigningKey_ExplicitKeyTakesPrecedence(t *testing.T) {
	// When an explicit key is provided, it should be used regardless
	// of what ExtractGitConfiguration might return from host .gitconfig.
	command := testBaseCommand
	explicitKey := "/explicit/key.pub"
	result := addGitSSHSigningKey(context.Background(), command, explicitKey, "")

	encoded := base64.StdEncoding.EncodeToString([]byte(explicitKey))
	assert.Equal(t, command+" --git-user-signing-key "+encoded, result)
}

func TestAddGitSSHSigningKey_EmptyExplicitKey_FallsBackToHostConfig(t *testing.T) {
	// Ensure deterministic environment with no host git signing config.
	command := testBaseCommand
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("XDG_CONFIG_HOME", tmpHome)

	result := addGitSSHSigningKey(context.Background(), command, "", "")

	assert.Equal(t, command, result)
	assert.NotContains(t, result, "--git-user-signing-key")
}

func TestBuildCredentialsCommand_IncludesSigningKey(t *testing.T) {
	opts := RunServicesOptions{
		User:                           "testuser",
		ConfigureGitSSHSignatureHelper: true,
		GitSSHSigningKey:               "/my/key.pub",
	}
	command := buildCredentialsCommand(context.Background(), opts)

	encoded := base64.StdEncoding.EncodeToString([]byte("/my/key.pub"))
	assert.Contains(t, command, "--git-user-signing-key "+encoded)
	assert.Contains(t, command, "--user testuser")
}

func TestBuildCredentialsCommand_NoSigningKey(t *testing.T) {
	opts := RunServicesOptions{
		User:                           "testuser",
		ConfigureGitSSHSignatureHelper: false,
	}
	command := buildCredentialsCommand(context.Background(), opts)

	assert.NotContains(t, command, "--git-user-signing-key")
}

func TestDisabledGitHubServicePreservesLegacyAgentCommand(t *testing.T) {
	opts := RunServicesOptions{
		User:                       "root",
		HostOwner:                  testServiceOwner,
		ConfigureGitCredentials:    true,
		ConfigureDockerCredentials: true,
	}
	command := buildCredentialsCommand(context.Background(), opts)
	assert.NotContains(t, command, "--host-owner")
	assert.NotContains(t, command, "--configure-github-helper")
	assert.Contains(t, command, "--configure-git-helper")
	assert.Contains(t, command, "--configure-docker-helper")
	opts.ConfigureGitHubCredentials = true
	command = buildCredentialsCommand(context.Background(), opts)
	assert.Contains(t, command, "--host-owner "+testServiceOwner)
	assert.Contains(t, command, "--configure-github-helper")
}

func TestOptionalGitHubServiceFailurePreservesUnrelatedServices(t *testing.T) {
	opts := RunServicesOptions{
		User: "vscode", HostOwner: testServiceOwner, ConfigureGitHubCredentials: true,
		ConfigureGitCredentials: true, ConfigureDockerCredentials: true,
		ConfigureGitSSHSignatureHelper: true, ForwardPorts: true,
	}
	calls := 0
	err := runOptionalGitHubServices(
		context.Background(),
		opts,
		func(current RunServicesOptions) error {
			calls++
			if current.ConfigureGitHubCredentials {
				return errUnsupportedGitHubServiceOptions
			}
			expected := opts
			expected.ConfigureGitHubCredentials = false
			expected.HostOwner = ""
			assert.Equal(t, expected, current)
			assert.NotContains(
				t,
				buildCredentialsCommand(context.Background(), current),
				"--host-owner",
			)
			return nil
		},
	)
	assert.NoError(t, err)
	assert.Equal(t, 2, calls)
}

func TestOptionalGitHubServiceDoesNotRetrySuccessDisabledOrCanceled(t *testing.T) {
	for _, mode := range []string{"success", disabledPreparation, "canceled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			calls := 0
			want := errors.New("startup unavailable")
			if mode == "success" {
				want = nil
			}
			err := runOptionalGitHubServices(
				ctx,
				RunServicesOptions{ConfigureGitHubCredentials: mode != disabledPreparation},
				func(RunServicesOptions) error {
					calls++
					return want
				},
			)
			assert.ErrorIs(t, err, want)
			assert.Equal(t, 1, calls)
		})
	}
}

func TestOptionalGitHubServicePreservesPolicyAfterUnrelatedFailure(t *testing.T) {
	for _, message := range []string{"SSH transport closed", "relay stopped after readiness", "unrelated helper failed"} {
		t.Run(message, func(t *testing.T) {
			want := errors.New(message)
			calls := 0
			opts := RunServicesOptions{
				ConfigureGitHubCredentials: true,
				HostOwner:                  testServiceOwner,
			}
			err := runOptionalGitHubServices(
				t.Context(),
				opts,
				func(current RunServicesOptions) error {
					calls++
					assert.Equal(t, opts, current)
					if !current.ConfigureGitHubCredentials {
						return nil
					}
					return want
				},
			)
			assert.ErrorIs(t, err, want)
			assert.Equal(t, 1, calls)
		})
	}
}
