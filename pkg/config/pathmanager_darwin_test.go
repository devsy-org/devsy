//go:build darwin

package config

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestDarwinPM() PathManager {
	pm := &darwinPathManager{}
	pm.pm = pm

	return pm
}

func TestDarwinConfigDir_IsUnderXDGConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	pm := newTestDarwinPM()

	got, err := pm.ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}

	want := filepath.Join(home, ".config", RepoName)
	if got != want {
		t.Errorf("ConfigDir = %q, want %q", got, want)
	}

	dataDir, err := pm.DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if got == dataDir {
		t.Errorf("ConfigDir and DataDir must be distinct, both = %q", got)
	}
}

func TestDarwinConfigFilePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvConfig, "")

	pm := newTestDarwinPM()
	got, err := pm.ConfigFilePath()
	if err != nil {
		t.Fatalf("ConfigFilePath: %v", err)
	}

	want := filepath.Join(home, ".config", RepoName, ConfigFile)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDarwinConfigFilePath_MigratesLegacyConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvConfig, "")

	pm := newTestDarwinPM()

	dataDir, err := pm.DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	legacyPath := filepath.Join(dataDir, ConfigFile)
	want := []byte("defaultContext: default\n")
	if err := os.WriteFile(legacyPath, want, 0o600); err != nil {
		t.Fatalf("seed legacy config: %v", err)
	}

	got, err := pm.ConfigFilePath()
	if err != nil {
		t.Fatalf("ConfigFilePath: %v", err)
	}

	newPath := filepath.Join(home, ".config", RepoName, ConfigFile)
	if got != newPath {
		t.Errorf("ConfigFilePath = %q, want %q", got, newPath)
	}

	migrated, err := os.ReadFile(newPath) // #nosec G304 -- test-controlled temp path
	if err != nil {
		t.Fatalf("read migrated config: %v", err)
	}
	if string(migrated) != string(want) {
		t.Errorf("migrated contents = %q, want %q", migrated, want)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Errorf("legacy config still present at %q (err=%v)", legacyPath, err)
	}
}

func TestDarwinConfigFilePath_KeepsExistingConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvConfig, "")

	pm := newTestDarwinPM()

	dataDir, err := pm.DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	legacyPath := filepath.Join(dataDir, ConfigFile)
	if err := os.WriteFile(legacyPath, []byte("legacy\n"), 0o600); err != nil {
		t.Fatalf("seed legacy config: %v", err)
	}

	newPath := filepath.Join(home, ".config", RepoName, ConfigFile)
	if err := os.MkdirAll(filepath.Dir(newPath), 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	want := []byte("current\n")
	if err := os.WriteFile(newPath, want, 0o600); err != nil {
		t.Fatalf("seed current config: %v", err)
	}

	if _, err := pm.ConfigFilePath(); err != nil {
		t.Fatalf("ConfigFilePath: %v", err)
	}

	got, err := os.ReadFile(newPath) // #nosec G304 -- test-controlled temp path
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("config was overwritten: got %q, want %q", got, want)
	}
}
