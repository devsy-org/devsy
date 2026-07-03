//go:build darwin

package config

import (
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
