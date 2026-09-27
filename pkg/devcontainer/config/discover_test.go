package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const rootConfigFilePath = ".devcontainer.json"

func TestDiscoverDevContainerPathDirectoryRootPrecedence(t *testing.T) {
	folder := t.TempDir()
	seedDiscoveryConfigs(
		t,
		folder,
		".devcontainer/devcontainer.json",
		rootConfigFilePath,
		".devcontainer/profile/devcontainer.json",
	)
	assertDiscoveredPath(t, folder, ".devcontainer/devcontainer.json")
}

func TestDiscoverDevContainerPathRootFilePrecedence(t *testing.T) {
	folder := t.TempDir()
	seedDiscoveryConfigs(t, folder, rootConfigFilePath, ".devcontainer/profile/devcontainer.json")
	assertDiscoveredPath(t, folder, rootConfigFilePath)
}

func TestDiscoverDevContainerPathSingleNested(t *testing.T) {
	folder := t.TempDir()
	seedDiscoveryConfigs(t, folder, ".devcontainer/profile/devcontainer.json")
	assertDiscoveredPath(t, folder, ".devcontainer/profile/devcontainer.json")
}

func TestDiscoverDevContainerPathAmbiguousNested(t *testing.T) {
	folder := t.TempDir()
	seedDiscoveryConfigs(
		t,
		folder,
		".devcontainer/one/devcontainer.json",
		".devcontainer/two/devcontainer.json",
	)
	path, err := DiscoverDevContainerPath(folder)
	if err == nil || !strings.Contains(err.Error(), "multiple devcontainer configurations found") {
		t.Fatalf("DiscoverDevContainerPath() = %q, %v, want ambiguity error", path, err)
	}
}

func TestDiscoverDevContainerPathNoConfig(t *testing.T) {
	assertDiscoveredPath(t, t.TempDir(), "")
}

func seedDiscoveryConfigs(t *testing.T, folder string, paths ...string) {
	t.Helper()
	for _, relativePath := range paths {
		file := filepath.Join(folder, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(`{"image":"test"}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func assertDiscoveredPath(t *testing.T, folder, want string) {
	t.Helper()
	got, err := DiscoverDevContainerPath(folder)
	if err != nil {
		t.Fatalf("DiscoverDevContainerPath(): %v", err)
	}
	if want == "" {
		if got != "" {
			t.Fatalf("DiscoverDevContainerPath() = %q, want empty", got)
		}
		return
	}
	wantPath := filepath.Join(folder, filepath.FromSlash(want))
	if got != wantPath {
		t.Fatalf("DiscoverDevContainerPath() = %q, want %q", got, wantPath)
	}
}
