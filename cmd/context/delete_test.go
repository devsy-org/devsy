package context

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envstore"
	"github.com/stretchr/testify/require"
)

func TestDeleteCmd_RemovesContextDirFromDisk(t *testing.T) {
	pkgconfig.ResetPathManager()
	t.Cleanup(pkgconfig.ResetPathManager)
	home := t.TempDir()
	t.Setenv(pkgconfig.EnvHome, home)
	t.Setenv(pkgconfig.EnvConfig, filepath.Join(home, "config.yaml"))

	// Seed a config.yaml with a second, non-default context so delete is legal.
	cfg := &pkgconfig.Config{
		DefaultContext: pkgconfig.DefaultContext,
		Contexts: map[string]*pkgconfig.ContextConfig{
			pkgconfig.DefaultContext: {},
			"staging":                {},
		},
	}
	if err := pkgconfig.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	contextDir, err := pkgconfig.DefaultPathManager().ContextDir("staging")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(
		filepath.Join(contextDir, "workspaces", "some-workspace"),
		0o750,
	); err != nil {
		t.Fatal(err)
	}

	cmd := &DeleteCmd{GlobalFlags: &flags.GlobalFlags{}}
	if err := cmd.Run(context.Background(), "staging"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(contextDir); !os.IsNotExist(err) {
		t.Fatalf("expected context dir %s to be removed, stat err = %v", contextDir, err)
	}
}

func setupDeleteContextTest(t *testing.T) (*pkgconfig.Config, string) {
	t.Helper()
	pkgconfig.ResetPathManager()
	t.Cleanup(pkgconfig.ResetPathManager)
	home := t.TempDir()
	t.Setenv(pkgconfig.EnvHome, home)
	t.Setenv(pkgconfig.EnvConfig, filepath.Join(home, "config.yaml"))
	cfg := &pkgconfig.Config{
		DefaultContext: pkgconfig.DefaultContext,
		Contexts: map[string]*pkgconfig.ContextConfig{
			pkgconfig.DefaultContext: {},
			"staging":                {EnvVars: []string{"ATTACHED"}},
		},
	}
	require.NoError(t, pkgconfig.SaveConfig(cfg))
	return cfg, home
}

func TestDeleteContextRemovesAttachedAndDetachedEnvironmentValues(t *testing.T) {
	cfg, home := setupDeleteContextTest(t)
	store, err := envstore.NewStoreForConfig(cfg)
	require.NoError(t, err)
	require.NoError(t, store.Set("staging", "ATTACHED", "attached"))
	require.NoError(t, store.Set("staging", "DETACHED", "detached"))
	require.NoError(t, store.Set(pkgconfig.DefaultContext, "PRESERVED", "default"))
	// An inaccessible secret store cannot affect cleanup of plaintext env values.
	require.NoError(
		t,
		os.WriteFile(filepath.Join(home, "secrets.yaml"), []byte("contexts: [broken"), 0o600),
	)
	require.NoError(t, os.WriteFile(filepath.Join(home, "secrets.enc"), []byte("corrupt"), 0o600))
	t.Setenv("DEVSY_SECRETS_PASSPHRASE", "")
	require.NoError(
		t,
		(&DeleteCmd{GlobalFlags: &flags.GlobalFlags{}}).Run(context.Background(), "staging"),
	)
	values, err := store.List("staging")
	require.NoError(t, err)
	require.Empty(t, values)
	value, err := store.Get(pkgconfig.DefaultContext, "PRESERVED")
	require.NoError(t, err)
	require.Equal(t, "default", value)
	cfg, err = pkgconfig.LoadConfig("", "")
	require.NoError(t, err)
	require.NotContains(t, cfg.Contexts, "staging")
}

func TestDeleteContextMigratesAndRemovesLegacyEnvironmentValues(t *testing.T) {
	cfg, home := setupDeleteContextTest(t)
	require.NoError(t, os.WriteFile(filepath.Join(home, "secrets.yaml"), []byte(`contexts:
  staging:
    LEGACY:
      kind: env
      value: legacy
      created: 2026-10-02T00:00:00Z
  default:
    PRESERVED:
      kind: env
      value: default
      created: 2026-10-02T00:00:00Z
`), 0o600))
	require.NoError(
		t,
		(&DeleteCmd{GlobalFlags: &flags.GlobalFlags{}}).Run(context.Background(), "staging"),
	)
	store, err := envstore.NewStoreForConfig(cfg)
	require.NoError(t, err)
	values, err := store.List("staging")
	require.NoError(t, err)
	require.Empty(t, values)
	value, err := store.Get(pkgconfig.DefaultContext, "PRESERVED")
	require.NoError(t, err)
	require.Equal(t, "default", value)
}

func TestDeleteContextRetainsConfigIfEnvironmentStoreUnreadable(t *testing.T) {
	_, home := setupDeleteContextTest(t)
	require.NoError(
		t,
		os.WriteFile(filepath.Join(home, envstore.FileName), []byte("schemaVersion: 999"), 0o600),
	)
	err := (&DeleteCmd{GlobalFlags: &flags.GlobalFlags{}}).Run(context.Background(), "staging")
	require.ErrorContains(t, err, "unsupported environment schema version")
	cfg, err := pkgconfig.LoadConfig("", "")
	require.NoError(t, err)
	require.Contains(t, cfg.Contexts, "staging")
}
