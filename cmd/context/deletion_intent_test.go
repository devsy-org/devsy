package context

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envstore"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/require"
)

func deletionRecoveryFixture(
	t *testing.T,
	credential ...string,
) (*config.Config, envstore.BatchStore, secrets.SecretStore) {
	t.Helper()
	cfg, _ := setupDeleteContextTest(t)
	cfg.Contexts[cleanupContext].Secrets = []string{cleanupFirstSecret, cleanupSecondSecret}
	require.NoError(t, config.SaveConfig(cfg))
	t.Setenv(secrets.EnvBackend, "file")
	t.Setenv(secrets.EnvPassphrase, "")
	t.Setenv(secrets.EnvPassphraseFile, "")
	if len(credential) > 0 {
		t.Setenv(secrets.EnvPassphrase, credential[0])
	}
	resolver := secrets.DefaultUnlockResolver{
		LookupEnv:      func(string) string { return "" },
		ReadRemembered: func() (string, error) { return "", nil },
	}
	if len(credential) > 0 {
		resolver.ExplicitPassphrase = credential[0]
	}
	store, err := secrets.NewSecretStoreForConfig(
		cfg,
		secrets.StoreOptions{UnlockResolver: resolver},
	)
	require.NoError(t, err)
	for _, name := range []string{cleanupFirstSecret, cleanupSecondSecret, cleanupDetachedSecret} {
		require.NoError(t, store.Set(cleanupContext, name, cleanupSecret))
	}
	envs, err := envstore.NewStoreForConfig(cfg)
	require.NoError(t, err)
	require.NoError(t, envs.Set(cleanupContext, cleanupAttached, cleanupEnv))
	require.NoError(t, envs.Set(cleanupContext, "DETACHED", "detached-env"))
	require.NoError(t, envs.Set("default", cleanupKeepName, "other-context"))
	dir, err := config.DefaultPathManager().ContextDir(cleanupContext)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(dir, "marker"), []byte("context workspace"), 0o600),
	)
	cfg, err = config.LoadConfig("", "")
	require.NoError(t, err)
	return cfg, envs.(envstore.BatchStore), store
}

func crashContextDeletion(t *testing.T, cfg *config.Config, phase string) {
	t.Helper()
	request, err := newContextDeleteRequest(cfg, cleanupContext)
	require.NoError(t, err)
	request.checkpoint = func(actual string) {
		if actual == phase {
			panic("simulated process exit")
		}
	}
	require.PanicsWithValue(t, "simulated process exit", func() {
		unlock, err := config.LockConfigForContextDeletion(cleanupContext)
		require.NoError(t, err)
		defer unlock()
		_ = deleteContextValues(request)
	})
	require.ErrorIs(t, config.CheckPendingContextDeletion(), config.ErrContextDeletionPending)
	path, err := config.GetConfigPath()
	require.NoError(t, err)
	// #nosec G304 -- journal fixture in the temporary config directory.
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(path), config.ContextDeletionIntentFile))
	require.NoError(t, err)
	require.NotContains(t, string(raw), cleanupSecret)
	require.NotContains(t, string(raw), cleanupEnv)
}

func assertForwardContextDeletion(
	t *testing.T,
	envs envstore.BatchStore,
	store secrets.SecretStore,
) {
	t.Helper()
	require.NoError(
		t,
		(&DeleteCmd{GlobalFlags: &flags.GlobalFlags{}}).Run(context.Background(), cleanupContext),
	)
	require.NoError(t, config.CheckPendingContextDeletion())
	cfg, err := config.LoadConfig("", "")
	require.NoError(t, err)
	require.NotContains(t, cfg.Contexts, cleanupContext)
	for _, name := range []string{cleanupFirstSecret, cleanupSecondSecret} {
		_, err := store.Meta(cleanupContext, name)
		require.ErrorIs(t, err, secrets.ErrSecretNotFound)
	}
	_, err = store.Meta(cleanupContext, cleanupDetachedSecret)
	require.NoError(t, err, "detached secrets are outside the original deletion scope")
	values, err := envs.List(cleanupContext)
	require.NoError(t, err)
	require.Empty(t, values)
	value, err := envs.Meta("default", cleanupKeepName)
	require.NoError(t, err)
	require.Equal(t, "other-context", value.Value)
	dir, err := config.DefaultPathManager().ContextDir(cleanupContext)
	require.NoError(t, err)
	_, err = os.Stat(dir)
	require.True(t, os.IsNotExist(err))
}

func TestContextDeletionForwardRecoveryAtEveryCrashBoundary(t *testing.T) {
	for _, phase := range []string{"intent", "secret", "environment", "config", "directory"} {
		t.Run(phase, func(t *testing.T) {
			cfg, envs, store := deletionRecoveryFixture(t)
			crashContextDeletion(t, cfg, phase)
			assertForwardContextDeletion(t, envs, store)
		})
	}
}

func TestContextDeletionIntentFailurePrecedesEveryMutation(t *testing.T) {
	request, envs, store := cleanupFixture(t)
	request.intent = &contextDeletionPersistence{
		begin: func(contextSnapshot) error { return errors.New("write failure: private-secret") },
	}
	err := deleteContextValues(request)
	require.ErrorContains(t, err, "deletion intent persistence")
	require.NotContains(t, err.Error(), cleanupSecret)
	require.Empty(t, store.deleted)
	require.Zero(t, envs.deleteCalls)
	require.Contains(t, request.config.Contexts, cleanupContext)
}

func TestContextDeletionClearFailureLeavesRecoverableIntent(t *testing.T) {
	cfg, envs, store := deletionRecoveryFixture(t)
	request, err := newContextDeleteRequest(cfg, cleanupContext)
	require.NoError(t, err)
	request.intent.clear = func() error { return errors.New("injected clear failure: private-secret") }
	err = deleteContextValues(request)
	require.ErrorIs(t, err, config.ErrContextDeletionPending)
	require.NotContains(t, err.Error(), cleanupSecret)
	require.ErrorIs(t, config.CheckPendingContextDeletion(), config.ErrContextDeletionPending)
	assertForwardContextDeletion(t, envs, store)
}

func TestContextDeletionSynchronousRollbackClearsIntent(t *testing.T) {
	cfg, _, store := deletionRecoveryFixture(t)
	request, err := newContextDeleteRequest(cfg, cleanupContext)
	require.NoError(t, err)
	request.envs = &cleanupEnvStore{
		BatchStore: request.envs,
		deleteErr:  errors.New("injected environment write failure"),
	}
	err = deleteContextValues(request)
	require.ErrorContains(t, err, "environment deletion")
	require.NoError(t, config.CheckPendingContextDeletion())
	for _, name := range []string{cleanupFirstSecret, cleanupSecondSecret} {
		value, err := store.Get(cleanupContext, name)
		require.NoError(t, err)
		require.Equal(t, cleanupSecret, value)
	}
	persisted, err := config.LoadConfig("", "")
	require.NoError(t, err)
	require.Contains(t, persisted.Contexts, cleanupContext)
}

func TestContextDeletionRecoveryRefusesRecreatedContext(t *testing.T) {
	cfg, envs, store := deletionRecoveryFixture(t)
	crashContextDeletion(t, cfg, "intent")
	// Simulate an external editor; normal config mutations are fenced.
	cfg.Contexts[cleanupContext] = &config.ContextConfig{EnvVars: []string{"RECREATED"}}
	require.NoError(t, config.SaveConfig(cfg))
	err := (&DeleteCmd{GlobalFlags: &flags.GlobalFlags{}}).Run(context.Background(), cleanupContext)
	require.ErrorIs(t, err, config.ErrContextDeletionPending)
	require.ErrorContains(t, err, "registered context changed")
	_, err = store.Meta(cleanupContext, cleanupFirstSecret)
	require.NoError(t, err)
	_, err = envs.Meta(cleanupContext, cleanupAttached)
	require.NoError(t, err)
	require.ErrorIs(t, config.CheckPendingContextDeletion(), config.ErrContextDeletionPending)
}

func TestContextDeletionRecoveryRejectsChangedSecretOwnership(t *testing.T) {
	cfg, _, _ := deletionRecoveryFixture(t)
	crashContextDeletion(t, cfg, "intent")
	intent, err := config.ReadContextDeletionIntent()
	require.NoError(t, err)
	intent.Secrets[0].Backend = "keyring"
	require.NoError(t, config.WriteContextDeletionIntent(intent))
	err = (&DeleteCmd{GlobalFlags: &flags.GlobalFlags{}}).Run(context.Background(), cleanupContext)
	require.ErrorIs(t, err, config.ErrContextDeletionPending)
	require.NotContains(t, err.Error(), cleanupSecret)
	require.ErrorIs(t, config.CheckPendingContextDeletion(), config.ErrContextDeletionPending)
}

func TestContextDeletionRecoveryRequiresOriginalUnlockCredential(t *testing.T) {
	const passphrase = "recovery-test-long-passphrase"
	cfg, envs, store := deletionRecoveryFixture(t, passphrase)
	crashContextDeletion(t, cfg, "intent")
	t.Setenv(secrets.EnvPassphrase, "")
	err := (&DeleteCmd{GlobalFlags: &flags.GlobalFlags{}}).Run(context.Background(), cleanupContext)
	require.ErrorIs(t, err, config.ErrContextDeletionPending)
	require.ErrorIs(t, err, secrets.ErrUnlockRequired)
	require.NotContains(t, err.Error(), cleanupSecret)
	require.NotContains(t, err.Error(), passphrase)
	require.ErrorIs(t, config.CheckPendingContextDeletion(), config.ErrContextDeletionPending)
	t.Setenv(secrets.EnvPassphrase, passphrase)
	assertForwardContextDeletion(t, envs, store)
}

func TestContextDeletionHonorsInitialContextFlagSelection(t *testing.T) {
	setupDeleteContextTest(t)
	cmd := &DeleteCmd{GlobalFlags: &flags.GlobalFlags{Context: cleanupContext}}
	require.NoError(t, cmd.Run(context.Background(), ""))
	cfg, err := config.LoadConfig("", "")
	require.NoError(t, err)
	require.NotContains(t, cfg.Contexts, cleanupContext)
	require.Contains(t, cfg.Contexts, config.DefaultContext)
}

func TestContextDeletionRecoveryRefusesChangedDataDirectory(t *testing.T) {
	cfg, _, store := deletionRecoveryFixture(t)
	crashContextDeletion(t, cfg, "intent")
	originalDir, err := config.DefaultPathManager().ContextDir(cleanupContext)
	require.NoError(t, err)
	t.Setenv(config.EnvHome, t.TempDir())
	err = (&DeleteCmd{GlobalFlags: &flags.GlobalFlags{}}).Run(context.Background(), cleanupContext)
	require.ErrorIs(t, err, config.ErrContextDeletionIntentInvalid)
	_, err = os.Stat(filepath.Join(originalDir, "marker"))
	require.NoError(
		t,
		err,
		"a different data root must never remove the original context directory",
	)
	_, err = store.Meta(cleanupContext, cleanupFirstSecret)
	require.NoError(t, err, "validation must precede any value deletion")
}
