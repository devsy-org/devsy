package up

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envstore"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/stretchr/testify/require"
)

func preflightConfig(t *testing.T) *config.Config {
	t.Helper()
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
	t.Setenv(config.EnvHome, t.TempDir())
	t.Setenv(secrets.EnvBackend, "file")
	t.Setenv(secrets.EnvPassphrase, "preflight-test-long-passphrase")
	t.Setenv(secrets.EnvPassphraseFile, "")
	return testConfig()
}

func TestPreflightAggregatesLockedAndMissingBeforeWorkspaceResolution(t *testing.T) {
	cfg := preflightConfig(t)
	store, err := secrets.NewSecretStoreForConfig(cfg)
	require.NoError(t, err)
	require.NoError(t, store.Set(cfg.DefaultContext, "LOCKED", "never disclose"))
	cfg.Current().Secrets = []string{"LOCKED", "MISSING"}
	t.Setenv(secrets.EnvPassphrase, "")
	options := secrets.StoreOptions{
		UnlockResolver: secrets.DefaultUnlockResolver{
			ReadRemembered: func() (string, error) { return "", nil },
		},
	}
	cmd := &UpCmd{secretOptions: &options}
	err = cmd.preflightLocalValues(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "2 unavailable values")
	require.Contains(t, err.Error(), "LOCKED  locked")
	require.Contains(t, err.Error(), "MISSING  missing")
	require.NotContains(t, err.Error(), "never disclose")
	require.ErrorIs(t, err, secrets.ErrUnlockRequired)
	require.ErrorIs(t, err, secrets.ErrSecretNotFound)
}

func TestWorkspaceEnvironmentResolutionIgnoresBrokenSecretBackend(t *testing.T) {
	cfg := preflightConfig(t)
	envs, err := envstore.NewStoreForConfig(cfg)
	require.NoError(t, err)
	require.NoError(t, envs.Set(cfg.DefaultContext, workspaceEnvLogLevel, "debug"))
	cfg.Current().EnvVars = []string{workspaceEnvLogLevel}
	path, err := config.GetConfigPath()
	require.NoError(t, err)
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(filepath.Dir(path), secrets.EncryptedFileName),
			[]byte("corrupt ciphertext"),
			0o600,
		),
	)
	t.Setenv(secrets.EnvPassphrase, "incorrect credential")
	cmd := &UpCmd{}
	require.NoError(t, cmd.preflightLocalValues(cfg))
	resolver, err := secrets.NewResolverForConfig(cfg)
	require.NoError(t, err)
	require.NoError(t, cmd.applyEnvVars(context.Background(), cfg, resolver))
	require.Contains(t, cmd.WorkspaceEnv, "LOG_LEVEL=debug")
}

func TestPreflightDefersProjectSourcesAndHonorsExplicitEnvOverride(t *testing.T) {
	cfg := preflightConfig(t)
	cfg.Current().Secrets = []string{"sops:project/TOKEN"}
	cfg.Current().EnvVars = []string{"OPTIONAL"}
	cmd := &UpCmd{}
	cmd.WorkspaceEnv = []string{"OPTIONAL=explicit"}
	require.NoError(t, cmd.preflightLocalValues(cfg))
}

func TestPreflightPreservesTypedFailureThroughAggregate(t *testing.T) {
	err := &unavailableValuesError{
		values: []unavailableValue{{secretToken, "locked"}},
		causes: []error{
			&secrets.UnlockFailedError{
				Backend: secrets.BackendFile,
				Cause:   errors.New("decryption failed"),
			},
		},
	}
	require.ErrorIs(t, err, secrets.ErrUnlockFailed)
}

func TestRequiredSecretNeverFallsBackToSameNamedEnvironmentValue(t *testing.T) {
	for _, attached := range []bool{true, false} {
		name := "explicit secret"
		if attached {
			name = "attached secret"
		}
		t.Run(name, func(t *testing.T) {
			cfg := preflightConfig(t)
			envs, err := envstore.NewStoreForConfig(cfg)
			require.NoError(t, err)
			require.NoError(
				t,
				envs.Set(cfg.DefaultContext, secretToken, "plaintext-must-not-substitute"),
			)
			cmd := &UpCmd{}
			if attached {
				cfg.Current().Secrets = []string{secretToken}
			} else {
				cmd.Secrets = []string{secretToken}
			}
			err = cmd.preflightLocalValues(cfg)
			require.ErrorIs(t, err, secrets.ErrSecretNotFound)
			require.Contains(t, err.Error(), "TOKEN  missing")
			require.NotContains(t, err.Error(), "plaintext-must-not-substitute")
			// Protect the later resolution path too, even if an early caller
			// skipped preflight or the secret disappeared after preflight.
			err = cmd.resolveStoredSecrets(context.Background(), cfg, nil)
			require.ErrorIs(t, err, secrets.ErrSecretNotFound)
			require.Empty(t, cmd.SecretsEnv)
			require.Empty(t, cmd.SecretsMount)
			value, err := envs.Get(cfg.DefaultContext, secretToken)
			require.NoError(t, err)
			require.Equal(t, "plaintext-must-not-substitute", value)
		})
	}
}

func TestEnvironmentOnlyWorkspacePreparationIgnoresBrokenSecretSources(t *testing.T) {
	for _, sourceConfig := range []struct{ name, contents string }{
		{"malformed registry", "contexts: [broken"},
		{"unsupported source", "contexts:\n  default:\n    - name: unsupported\n      type: unsupported\n"},
	} {
		t.Run(sourceConfig.name, func(t *testing.T) {
			cfg := preflightConfig(t)
			envs, err := envstore.NewStoreForConfig(cfg)
			require.NoError(t, err)
			require.NoError(t, envs.Set(cfg.DefaultContext, workspaceEnvLogLevel, "debug"))
			cfg.Current().EnvVars = []string{workspaceEnvLogLevel}
			path, err := config.GetConfigPath()
			require.NoError(t, err)
			dir := filepath.Dir(path)
			require.NoError(
				t,
				os.WriteFile(
					filepath.Join(dir, "secret-sources.yaml"),
					[]byte(sourceConfig.contents),
					0o600,
				),
			)
			require.NoError(
				t,
				os.WriteFile(
					filepath.Join(dir, secrets.IndexFileName),
					[]byte("contexts: [broken"),
					0o600,
				),
			)
			require.NoError(
				t,
				os.WriteFile(
					filepath.Join(dir, secrets.EncryptedFileName),
					[]byte("corrupt ciphertext"),
					0o600,
				),
			)
			t.Setenv(secrets.EnvPassphrase, "")
			t.Setenv(secrets.EnvPassphraseFile, "/nonexistent")
			cmd := &UpCmd{}
			require.NoError(t, cmd.preflightLocalValues(cfg))
			require.NoError(t, cmd.prepareSecretsWithProject(context.Background(), cfg, nil))
			require.Contains(t, cmd.WorkspaceEnv, "LOG_LEVEL=debug")
			require.Empty(t, cmd.SecretsEnv)
			require.Empty(t, cmd.SecretsMount)
			// The isolation check exercised broken source configuration rather
			// than silently ignoring a source registry stored at the wrong path.
			_, err = secrets.NewResolverForConfig(cfg)
			require.Error(t, err)
		})
	}
}

func TestDetachedSubmissionFailsBeforeCreatingTaskWhenSecretLocked(t *testing.T) {
	cfg := preflightConfig(t)
	store, err := secrets.NewSecretStoreForConfig(cfg)
	require.NoError(t, err)
	require.NoError(t, store.Set(cfg.DefaultContext, "DETACHED_LOCKED", "private test value"))
	cfg.Current().Secrets = []string{"DETACHED_LOCKED"}
	t.Setenv(secrets.EnvPassphrase, "")
	options := secrets.StoreOptions{
		UnlockResolver: secrets.DefaultUnlockResolver{
			ReadRemembered: func() (string, error) { return "", nil },
		},
	}
	cmd := &UpCmd{secretOptions: &options}
	require.ErrorIs(t, cmd.runDetached([]string{"."}, cfg), secrets.ErrUnlockRequired)
	tasks, err := task.NewStore()
	require.NoError(t, err)
	entries, err := tasks.List()
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestDetachedCredentialOnlyScopesAlreadyResolvedMaterialToChild(t *testing.T) {
	t.Setenv(secrets.EnvPassphrase, "detached-session-credential")
	t.Setenv(secrets.EnvPassphraseFile, "")
	cmd := &UpCmd{}
	options := cmd.unlockOptions()
	_, err := options.UnlockResolver.ResolvePassphrase(
		t.Context(),
		secrets.UnlockRequest{Purpose: "open"},
	)
	require.NoError(t, err)
	t.Setenv(secrets.EnvPassphrase, "")
	t.Setenv(secrets.EnvPassphraseFile, "/must-not-read-this-file")
	passphrase, filePath := "", ""
	for _, entry := range cmd.detachedInvocationEnvironment() {
		name, value, _ := strings.Cut(entry, "=")
		if name == secrets.EnvPassphrase {
			passphrase = value
		}
		if name == secrets.EnvPassphraseFile {
			filePath = value
		}
	}
	require.Equal(t, "detached-session-credential", passphrase)
	require.Empty(t, filePath)
	require.Empty(t, os.Getenv(secrets.EnvPassphrase))
	require.Equal(t, "/must-not-read-this-file", os.Getenv(secrets.EnvPassphraseFile))
}
