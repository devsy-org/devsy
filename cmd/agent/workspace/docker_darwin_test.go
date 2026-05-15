//go:build darwin

package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindDarwinDocker_FoundAtKnownPath(t *testing.T) {
	// Create a temp directory with a fake docker binary.
	tmpDir := t.TempDir()
	fakeBin := filepath.Join(tmpDir, "docker")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("failed to create fake docker binary: %v", err)
	}

	// Override the package-level path list so that our temp path is checked.
	original := darwinDockerPaths
	darwinDockerPaths = []string{fakeBin}
	t.Cleanup(func() { darwinDockerPaths = original })

	path, err := findDarwinDocker()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if path != fakeBin {
		t.Fatalf("expected path %q, got %q", fakeBin, path)
	}
}

func TestFindDarwinDocker_NotFound(t *testing.T) {
	// Point at paths that definitely don't exist.
	original := darwinDockerPaths
	darwinDockerPaths = []string{"/nonexistent/path/docker"}
	t.Cleanup(func() { darwinDockerPaths = original })

	_, err := findDarwinDocker()
	if err == nil {
		t.Fatal("expected an error when no docker binary exists")
	}
	if !strings.Contains(err.Error(), "Docker Desktop") {
		t.Fatalf("error should mention Docker Desktop, got: %v", err)
	}
	if strings.Contains(err.Error(), "unsupported OS") {
		t.Fatalf("error must NOT mention unsupported OS, got: %v", err)
	}
}

func TestFindDarwinDocker_PrefersFirstPath(t *testing.T) {
	tmpDir := t.TempDir()
	first := filepath.Join(tmpDir, "docker-first")
	second := filepath.Join(tmpDir, "docker-second")
	for _, p := range []string{first, second} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("failed to create fake binary: %v", err)
		}
	}

	original := darwinDockerPaths
	darwinDockerPaths = []string{first, second}
	t.Cleanup(func() { darwinDockerPaths = original })

	path, err := findDarwinDocker()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if path != first {
		t.Fatalf("expected first path %q, got %q", first, path)
	}
}
