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
