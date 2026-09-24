package env

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
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

func mustLoadConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := config.LoadConfig("", "")
	require.NoError(t, err)
	return cfg
}
