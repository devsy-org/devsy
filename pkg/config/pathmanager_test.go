package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultPathManagerSingleton(t *testing.T) {
	ResetPathManager()
	t.Cleanup(ResetPathManager)

	pm1 := DefaultPathManager()
	pm2 := DefaultPathManager()

	if pm1 != pm2 {
		t.Error("DefaultPathManager returned different instances")
	}
}

func TestSetPathManagerOverride(t *testing.T) {
	ResetPathManager()
	t.Cleanup(ResetPathManager)

	original := DefaultPathManager()

	custom := NewPathManager()
	SetPathManager(custom)

	got := DefaultPathManager()
	if got != custom {
		t.Error("SetPathManager did not override the singleton")
	}
	if got == original {
		t.Error("SetPathManager did not replace the original instance")
	}
}

func TestResetPathManager(t *testing.T) {
	ResetPathManager()
	t.Cleanup(ResetPathManager)

	pm1 := DefaultPathManager()
	ResetPathManager()
	pm2 := DefaultPathManager()

	if pm1 == pm2 {
		t.Error("ResetPathManager did not clear the singleton — same instance returned")
	}
}

func TestDevsyHomeOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, home)

	pm := NewPathManager()

	tests := []struct {
		name string
		fn   func() (string, error)
		want string
	}{
		{"ConfigDir", pm.ConfigDir, home},
		{"DataDir", pm.DataDir, home},
		{"CacheDir", pm.CacheDir, filepath.Join(home, "cache")},
		{"StateDir", pm.StateDir, filepath.Join(home, "state")},
		{"RuntimeDir", pm.RuntimeDir, filepath.Join(home, "run")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.fn()
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", tt.name, err)
			}
			if got != tt.want {
				t.Errorf("%s = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

func TestDevsyHomeOverrideConfigFilePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, home)
	t.Setenv(EnvConfig, "")

	pm := NewPathManager()

	got, err := pm.ConfigFilePath()
	if err != nil {
		t.Fatalf("ConfigFilePath: %v", err)
	}

	want := filepath.Join(home, ConfigFile)
	if got != want {
		t.Errorf("ConfigFilePath = %q, want %q", got, want)
	}
}

func TestNewPathManagerReturnsNewInstance(t *testing.T) {
	pm1 := NewPathManager()
	pm2 := NewPathManager()

	if pm1 == pm2 {
		t.Error("NewPathManager returned the same instance twice")
	}
}
