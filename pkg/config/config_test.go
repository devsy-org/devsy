package config

import "testing"

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
			DefaultContext: {EnvVars: []string{"LOG_LEVEL"}},
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
	loaded.Current().EnvVars = []string{"LOG_LEVEL"}
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
	if got.Contexts["staging"].EnvVars[0] != "LOG_LEVEL" {
		t.Fatalf("staging EnvVars = %#v", got.Contexts["staging"].EnvVars)
	}
}
