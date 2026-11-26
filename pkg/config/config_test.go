package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const testEnvName = "LOG_LEVEL"

func TestLoadConfig_StampsCurrentSchemaVersion(t *testing.T) {
	ResetPathManager()
	t.Cleanup(ResetPathManager)
	home := t.TempDir()
	t.Setenv(EnvHome, home)

	cfg, err := LoadConfig("", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf(
			"SchemaVersion = %d, want %d (a freshly created config must be stamped)",
			cfg.SchemaVersion,
			CurrentSchemaVersion,
		)
	}

	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	reloaded, err := LoadConfig("", "")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf(
			"reloaded SchemaVersion = %d, want %d",
			reloaded.SchemaVersion,
			CurrentSchemaVersion,
		)
	}
}

func TestLoadConfig_StampsMissingSchemaVersionOnExistingConfig(t *testing.T) {
	ResetPathManager()
	t.Cleanup(ResetPathManager)
	home := t.TempDir()
	t.Setenv(EnvHome, home)

	// Simulate a pre-existing config.yaml written before this field existed.
	if err := SaveConfig(
		&Config{
			DefaultContext: DefaultContext,
			Contexts:       map[string]*ContextConfig{DefaultContext: {}},
		},
	); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig("", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf(
			"SchemaVersion = %d, want %d (an unstamped config loaded today must be treated as current)",
			cfg.SchemaVersion,
			CurrentSchemaVersion,
		)
	}
}

func TestContextConfigEnvVarsRoundTrip(t *testing.T) {
	ResetPathManager()
	t.Cleanup(ResetPathManager)
	t.Setenv(EnvHome, t.TempDir())
	want := &Config{
		DefaultContext: DefaultContext,
		Contexts: map[string]*ContextConfig{
			DefaultContext: {EnvVars: []string{testEnvName}},
		},
	}
	if err := SaveConfig(want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig("", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Current().EnvVars) != 1 || got.Current().EnvVars[0] != "LOG_LEVEL" {
		t.Fatalf("EnvVars = %#v, want [LOG_LEVEL]", got.Current().EnvVars)
	}
}

func TestSaveConfigRestoresTemporaryContextAndProviderOverrides(t *testing.T) {
	ResetPathManager()
	t.Cleanup(ResetPathManager)
	t.Setenv(EnvHome, t.TempDir())
	want := &Config{
		DefaultContext: DefaultContext,
		Contexts: map[string]*ContextConfig{
			DefaultContext: {DefaultProvider: "docker"},
			"staging":      {DefaultProvider: "kubernetes"},
		},
	}
	if err := SaveConfig(want); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadConfig("staging", "ssh")
	if err != nil {
		t.Fatal(err)
	}
	loaded.Current().EnvVars = []string{testEnvName}
	if err := SaveConfig(loaded); err != nil {
		t.Fatal(err)
	}

	got, err := LoadConfig("", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultContext != DefaultContext {
		t.Fatalf("DefaultContext = %q, want %q", got.DefaultContext, DefaultContext)
	}
	if got.Contexts["staging"].DefaultProvider != "kubernetes" {
		t.Fatalf("staging provider = %q, want kubernetes", got.Contexts["staging"].DefaultProvider)
	}
	if got.Contexts["staging"].EnvVars[0] != testEnvName {
		t.Fatalf("staging EnvVars = %#v", got.Contexts["staging"].EnvVars)
	}
}

func TestSaveConfigRestoresEmptyTemporaryProviderOverride(t *testing.T) {
	ResetPathManager()
	t.Cleanup(ResetPathManager)
	t.Setenv(EnvHome, t.TempDir())
	want := &Config{
		DefaultContext: DefaultContext,
		Contexts:       map[string]*ContextConfig{DefaultContext: {}},
	}
	if err := SaveConfig(want); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadConfig("", "ssh")
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveConfig(loaded); err != nil {
		t.Fatal(err)
	}

	got, err := LoadConfig("", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Current().DefaultProvider != "" {
		t.Fatalf("provider = %q, want empty", got.Current().DefaultProvider)
	}
}

func TestSaveConfigPreservesAbsoluteConfigSymlink(t *testing.T) {
	ResetPathManager()
	t.Cleanup(ResetPathManager)
	root := t.TempDir()
	targetDir := filepath.Join(root, "target")
	linkDir := filepath.Join(root, "link")
	require.NoError(t, os.MkdirAll(targetDir, 0o700))
	require.NoError(t, os.MkdirAll(linkDir, 0o700))
	targetPath := filepath.Join(targetDir, ConfigFile)
	linkPath := filepath.Join(linkDir, ConfigFile)
	require.NoError(t, os.WriteFile(targetPath, []byte("{}\n"), 0o600))
	require.NoError(t, os.Symlink(targetPath, linkPath))
	t.Setenv(EnvConfig, linkPath)

	require.NoError(t, SaveConfig(&Config{
		DefaultContext: DefaultContext,
		Contexts:       map[string]*ContextConfig{DefaultContext: {EnvVars: []string{testEnvName}}},
	}))

	info, err := os.Lstat(linkPath)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink)
	got, err := LoadConfig("", "")
	require.NoError(t, err)
	require.Equal(t, []string{testEnvName}, got.Current().EnvVars)
}

func TestSaveConfigPreservesRelativeConfigSymlink(t *testing.T) {
	ResetPathManager()
	t.Cleanup(ResetPathManager)
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	sharedDir := filepath.Join(root, "shared")
	require.NoError(t, os.MkdirAll(configDir, 0o700))
	require.NoError(t, os.MkdirAll(sharedDir, 0o700))
	targetPath := filepath.Join(sharedDir, ConfigFile)
	linkPath := filepath.Join(configDir, ConfigFile)
	require.NoError(t, os.WriteFile(targetPath, []byte("{}\n"), 0o600))
	const linkTarget = "../shared/config.yaml"
	require.NoError(t, os.Symlink(linkTarget, linkPath))
	t.Setenv(EnvConfig, linkPath)

	require.NoError(t, SaveConfig(&Config{
		DefaultContext: DefaultContext,
		Contexts:       map[string]*ContextConfig{DefaultContext: {EnvVars: []string{testEnvName}}},
	}))

	info, err := os.Lstat(linkPath)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink)
	gotTarget, err := os.Readlink(linkPath)
	require.NoError(t, err)
	require.Equal(t, linkTarget, gotTarget)
	got, err := LoadConfig("", "")
	require.NoError(t, err)
	require.Equal(t, []string{testEnvName}, got.Current().EnvVars)
}

func TestSaveConfigDoesNotReplaceDanglingConfigSymlink(t *testing.T) {
	ResetPathManager()
	t.Cleanup(ResetPathManager)
	root := t.TempDir()
	linkPath := filepath.Join(root, ConfigFile)
	require.NoError(t, os.Symlink("missing.yaml", linkPath))
	t.Setenv(EnvConfig, linkPath)

	err := SaveConfig(&Config{
		DefaultContext: DefaultContext,
		Contexts:       map[string]*ContextConfig{DefaultContext: {}},
	})
	require.Error(t, err)
	info, statErr := os.Lstat(linkPath)
	require.NoError(t, statErr)
	require.NotZero(t, info.Mode()&os.ModeSymlink)
}
