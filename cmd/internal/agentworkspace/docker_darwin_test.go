//go:build darwin

package agentworkspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestFindDarwinDockerCLIAtKnownPath(t *testing.T) {
	fakeBin := filepath.Join(t.TempDir(), "docker")
	writeExecutable(t, fakeBin)
	path, err := findDarwinDockerCLIInPaths([]string{fakeBin})
	if err != nil || path != fakeBin {
		t.Fatalf("find docker CLI = %q, %v; want %q", path, err, fakeBin)
	}
}

func TestFindDarwinDockerCLIRancherDesktopPath(t *testing.T) {
	home := t.TempDir()
	rancher := filepath.Join(home, ".rd", "bin", "docker")
	writeExecutable(t, rancher)
	path, err := findDarwinDockerCLIInPaths(darwinDockerCandidatePaths(home))
	if err != nil || path != rancher {
		t.Fatalf("find docker CLI = %q, %v; want %q", path, err, rancher)
	}
}

func TestFindDarwinDockerCLIPreservesPrecedence(t *testing.T) {
	tmp := t.TempDir()
	static, rancher := filepath.Join(
		tmp,
		"static",
		"docker",
	), filepath.Join(
		tmp,
		".rd",
		"bin",
		"docker",
	)
	writeExecutable(t, static)
	writeExecutable(t, rancher)
	path, err := findDarwinDockerCLIInPaths([]string{static, rancher})
	if err != nil || path != static {
		t.Fatalf("find docker CLI = %q, %v; want %q", path, err, static)
	}
}

func TestFindDarwinDockerCLINotFound(t *testing.T) {
	_, err := findDarwinDockerCLIInPaths([]string{filepath.Join(t.TempDir(), "docker")})
	if err == nil || !strings.Contains(err.Error(), "docker CLI") ||
		!strings.Contains(err.Error(), "DOCKER_PATH") {
		t.Fatalf("expected actionable docker CLI error, got %v", err)
	}
	if strings.Contains(err.Error(), "install Docker Desktop") {
		t.Fatalf("error must not require Docker Desktop: %v", err)
	}
}

func TestFindDarwinDockerCLIHomeFailureDoesNotBreakStaticDiscovery(t *testing.T) {
	static := filepath.Join(t.TempDir(), "docker")
	writeExecutable(t, static)
	paths := append([]string{static}, darwinDockerCandidatePaths("")...)
	path, err := findDarwinDockerCLIInPaths(paths)
	if err != nil || path != static {
		t.Fatalf("find docker CLI = %q, %v; want %q", path, err, static)
	}
}

func TestFindDarwinDockerCLIRejectsNonExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := findDarwinDockerCLIInPaths([]string{path}); err == nil {
		t.Fatal("expected non-executable candidate to be rejected")
	}
}

func TestFindDarwinDockerCLIRejectsInaccessibleCandidate(t *testing.T) {
	tmp := t.TempDir()
	inaccessible := filepath.Join(tmp, "inaccessible", "docker")
	writeExecutable(t, inaccessible)
	if err := os.Chmod(inaccessible, 0o001); err != nil {
		t.Fatal(err)
	}

	valid := filepath.Join(tmp, "valid", "docker")
	writeExecutable(t, valid)

	path, err := findDarwinDockerCLIInPaths([]string{inaccessible, valid})
	if err != nil || path != valid {
		t.Fatalf("expected fallback to valid candidate %q, got %q (err: %v)", valid, path, err)
	}
}
