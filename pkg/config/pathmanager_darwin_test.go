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

func TestDarwinConfigDir_SharesDataRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	pm := newTestDarwinPM()

	got, err := pm.ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}

	want := filepath.Join(home, "."+RepoName)
	if got != want {
		t.Errorf("ConfigDir = %q, want %q", got, want)
	}

	dataDir, err := pm.DataDir()
	if err != nil {
		t.Fatalf("DataDir: %v", err)
	}
	if got != dataDir {
		t.Errorf("ConfigDir and DataDir must share the same root: config=%q data=%q", got, dataDir)
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

	want := filepath.Join(home, "."+RepoName, ConfigFile)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
