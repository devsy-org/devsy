package env

import (
	"context"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envstore"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/stretchr/testify/require"
)

func setupEnvCommandTest(t *testing.T) *flags.GlobalFlags {
	t.Helper()
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
	t.Setenv(config.EnvHome, t.TempDir())
	t.Setenv("DEVSY_SECRETS_BACKEND", "file")
	t.Setenv("DEVSY_SECRETS_PASSPHRASE", "test-passphrase")
	require.NoError(t, config.SaveConfig(&config.Config{
		DefaultContext: config.DefaultContext,
		Contexts:       map[string]*config.ContextConfig{config.DefaultContext: {}},
	}))
	return &flags.GlobalFlags{}
}

func TestAttachAndDetachEnvironmentVariable(t *testing.T) {
	globalFlags := setupEnvCommandTest(t)
	set := &SetCmd{GlobalFlags: globalFlags, Value: "debug"}
	require.NoError(t, set.Run(context.Background(), "LOG_LEVEL"))

	attach := &AttachCmd{GlobalFlags: globalFlags}
	require.NoError(t, attach.Run(context.Background(), "LOG_LEVEL"))
	require.NoError(t, attach.Run(context.Background(), "LOG_LEVEL"))
	cfg, err := config.LoadConfig("", "")
	require.NoError(t, err)
	require.Equal(t, []string{"LOG_LEVEL"}, cfg.Current().EnvVars)

	detach := &DetachCmd{GlobalFlags: globalFlags}
	require.NoError(t, detach.Run(context.Background(), "LOG_LEVEL"))
	require.NoError(t, detach.Run(context.Background(), "LOG_LEVEL"))
	cfg, err = config.LoadConfig("", "")
	require.NoError(t, err)
	require.Empty(t, cfg.Current().EnvVars)
}

func TestAttachRejectsSecret(t *testing.T) {
	globalFlags := setupEnvCommandTest(t)
	store, err := secrets.NewStoreForConfig(mustLoadConfig(t))
	require.NoError(t, err)
	require.NoError(t, store.Set(config.DefaultContext, "API_TOKEN", "secret", secrets.KindSecret))
	err = (&AttachCmd{GlobalFlags: globalFlags}).Run(context.Background(), "API_TOKEN")
	require.Error(t, err)
	require.Contains(t, err.Error(), "is a secret")
}

func TestDeleteUnbindsEnvironmentVariable(t *testing.T) {
	globalFlags := setupEnvCommandTest(t)
	set := &SetCmd{GlobalFlags: globalFlags, Value: "debug"}
	require.NoError(t, set.Run(context.Background(), "LOG_LEVEL"))
	require.NoError(
		t,
		(&AttachCmd{GlobalFlags: globalFlags}).Run(context.Background(), "LOG_LEVEL"),
	)
	require.NoError(
		t,
		(&DeleteCmd{GlobalFlags: globalFlags}).Run(context.Background(), "LOG_LEVEL"),
	)
	cfg, err := config.LoadConfig("", "")
	require.NoError(t, err)
	require.Empty(t, cfg.Current().EnvVars)
}

func TestSetRejectsConvertingAttachedSecretToEnvironment(t *testing.T) {
	globalFlags := setupEnvCommandTest(t)
	cfg := mustLoadConfig(t)
	store, err := secrets.NewStoreForConfig(cfg)
	require.NoError(t, err)
	require.NoError(t, store.Set(config.DefaultContext, "TOKEN", "secret", secrets.KindSecret))
	cfg.Current().Secrets = []string{"TOKEN"}
	require.NoError(t, config.SaveConfig(cfg))
	err = (&SetCmd{GlobalFlags: globalFlags, Value: "plain"}).Run(context.Background(), "TOKEN")
	require.Error(t, err)
	require.Contains(t, err.Error(), "attached as a secret")
}

func TestConcurrentAttachmentsPreserveBothChanges(t *testing.T) {
	globalFlags := setupEnvCommandTest(t)
	set := func(name string) {
		require.NoError(
			t,
			(&SetCmd{GlobalFlags: globalFlags, Value: name}).Run(context.Background(), name),
		)
	}
	set("FIRST")
	set("SECOND")

	var wg sync.WaitGroup
	for _, name := range []string{"FIRST", "SECOND"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			require.NoError(
				t,
				(&AttachCmd{GlobalFlags: globalFlags}).Run(context.Background(), name),
			)
		}(name)
	}
	wg.Wait()

	cfg, err := config.LoadConfig("", "")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"FIRST", "SECOND"}, cfg.Current().EnvVars)
}

func mustLoadConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.LoadConfig("", "")
	require.NoError(t, err)
	return cfg
}

func TestAttachEnvironmentRejectsSameNamedAttachedSecret(t *testing.T) {
	for _, reference := range []string{"TOKEN", "local:local/TOKEN"} {
		t.Run(reference, func(t *testing.T) {
			globalFlags := setupEnvCommandTest(t)
			cfg := mustLoadConfig(t)
			secretStore, err := secrets.NewSecretStoreForConfig(cfg)
			require.NoError(t, err)
			require.NoError(t, secretStore.Set(cfg.DefaultContext, "TOKEN", "protected"))
			envs, err := envstore.NewStoreForConfig(cfg)
			require.NoError(t, err)
			require.NoError(t, envs.Set(cfg.DefaultContext, "TOKEN", "plaintext"))
			cfg.Current().Secrets = []string{reference}
			require.NoError(t, config.SaveConfig(cfg))
			t.Setenv(secrets.EnvPassphrase, "")
			err = (&AttachCmd{GlobalFlags: globalFlags}).Run(context.Background(), "TOKEN")
			require.ErrorContains(t, err, "attached as a secret")
			cfg = mustLoadConfig(t)
			require.Empty(t, cfg.Current().EnvVars)
			require.Equal(t, []string{reference}, cfg.Current().Secrets)
			value, err := envs.Get(cfg.DefaultContext, "TOKEN")
			require.NoError(t, err)
			require.Equal(t, "plaintext", value)
		})
	}
}

func TestSetEnvironmentRejectsQualifiedAttachedSecret(t *testing.T) {
	for _, reference := range []string{"local:local/TOKEN", "local/TOKEN"} {
		t.Run(reference, func(t *testing.T) {
			globalFlags := setupEnvCommandTest(t)
			cfg := mustLoadConfig(t)
			cfg.Current().Secrets = []string{reference}
			require.NoError(t, config.SaveConfig(cfg))
			envs, err := envstore.NewStoreForConfig(cfg)
			require.NoError(t, err)
			require.NoError(t, envs.Set(cfg.DefaultContext, "TOKEN", "existing"))
			err = (&SetCmd{GlobalFlags: globalFlags, Value: "replacement"}).Run(
				context.Background(),
				"TOKEN",
			)
			require.ErrorContains(t, err, "attached as a secret")
			value, err := envs.Get(cfg.DefaultContext, "TOKEN")
			require.NoError(t, err)
			require.Equal(t, "existing", value)
		})
	}
}
