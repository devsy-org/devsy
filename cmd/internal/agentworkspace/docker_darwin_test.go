//go:build darwin

package agentworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
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
	if !slices.Contains(darwinDockerCandidatePaths(home), rancher) {
		t.Fatal("Rancher Desktop path missing from Docker CLI candidates")
	}
	// Use test-owned candidates so an installed Docker CLI cannot shadow the fixture.
	path, err := findDarwinDockerCLIInPaths(
		[]string{filepath.Join(home, "missing", "docker"), rancher},
	)
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

func TestDarwinDockerDiscoveryPrecedesDisabledInstallation(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	existing := filepath.Join(t.TempDir(), "docker")
	writeExecutable(t, existing)
	info := &provider.AgentWorkspaceInfo{}
	info.Agent.Docker.Install = types.StrBool("false")
	calls := 0
	initializer := &workspaceInitializer{
		workspaceInfo: info,
		findDarwinDocker: func() (string, error) {
			calls++
			return findDarwinDockerCLIInPaths([]string{existing})
		},
		installDocker: func(context.Context) (string, error) {
			t.Fatal("disabled installation called the installer")
			return "", nil
		},
	}
	path, err := initializer.ensureDockerInstalled(context.Background())
	if err != nil || path != existing || calls != 1 {
		t.Fatalf(
			"Docker CLI discovery = %q, %v, calls %d; want %q, nil, 1",
			path,
			err,
			calls,
			existing,
		)
	}

	initializer.findDarwinDocker = func() (string, error) {
		return "", errors.New("Docker CLI absent")
	}
	path, err = initializer.ensureDockerInstalled(context.Background())
	if path != "" || err == nil || !strings.Contains(err.Error(), "agent.docker.install is false") {
		t.Fatalf("missing Docker CLI = %q, %v; want disabled installation error", path, err)
	}
}
