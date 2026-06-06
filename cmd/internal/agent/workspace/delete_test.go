package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func skipIfPermissionsNotEnforced(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("permission test not applicable on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("test not meaningful when running as root")
	}
}

func chmodReadOnly(t *testing.T, path string) {
	t.Helper()
	err := os.Chmod(path, 0o500) // #nosec G302 -- intentional: testing restrictive perms
	if err != nil {
		t.Fatal(err)
	}
}

func assertRemoved(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected directory to be removed, got stat err: %v", err)
	}
}

func TestForceRemoveAll_RegularDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(filepath.Join(target, "content", "src"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(target, "content", "src", "main.go"),
		[]byte("package main"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	if err := forceRemoveAll(target); err != nil {
		t.Fatalf("forceRemoveAll failed: %v", err)
	}
	assertRemoved(t, target)
}

func TestForceRemoveAll_NonExistentPath(t *testing.T) {
	if err := forceRemoveAll("/tmp/nonexistent-path-that-does-not-exist-12345"); err != nil {
		t.Fatalf("forceRemoveAll on nonexistent path should not error: %v", err)
	}
}

func TestForceRemoveAll_ReadOnlyDirectory(t *testing.T) {
	skipIfPermissionsNotEnforced(t)

	dir := t.TempDir()
	target := filepath.Join(dir, "workspace")
	content := filepath.Join(target, "content")
	if err := os.MkdirAll(content, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "file.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Remove write+execute from content dir — simulates what crun does.
	chmodReadOnly(t, content)

	// Standard RemoveAll should fail.
	if err := os.RemoveAll(target); err == nil {
		t.Skip("os.RemoveAll succeeded unexpectedly — filesystem may not enforce permissions")
	}

	// forceRemoveAll should fix permissions and succeed.
	if err := forceRemoveAll(target); err != nil {
		t.Fatalf("forceRemoveAll failed: %v", err)
	}
	assertRemoved(t, target)
}

func TestForceRemoveAll_NestedReadOnlyDirectories(t *testing.T) {
	skipIfPermissionsNotEnforced(t)

	dir := t.TempDir()
	target := filepath.Join(dir, "workspace")
	deep := filepath.Join(target, "content", "a", "b", "c")
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "file.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Make several levels read-only (bottom-up to avoid locking ourselves out).
	for _, d := range []string{
		deep,
		filepath.Join(target, "content", "a", "b"),
		filepath.Join(target, "content", "a"),
		filepath.Join(target, "content"),
	} {
		chmodReadOnly(t, d)
	}

	if err := forceRemoveAll(target); err != nil {
		t.Fatalf("forceRemoveAll failed: %v", err)
	}
	assertRemoved(t, target)
}

func TestForceRemoveAll_EmptyString(t *testing.T) {
	if err := forceRemoveAll(""); err != nil {
		t.Fatalf("forceRemoveAll with empty string should not error: %v", err)
	}
}
