package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecuredContainerDataDir_CreatesWithExpectedPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "devsy-data")

	got := securedContainerDataDir(dir)
	if got != dir {
		t.Fatalf("securedContainerDataDir() = %q, want %q", got, dir)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %s: %v", dir, err)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Errorf("mode = %o, want 0755", perm)
	}
}

// TestSecuredContainerDataDir_NarrowsPreExistingLaxPermissions is a
// regression test: /tmp is world-writable, so another user inside the same
// container could pre-create the fallback directory with lax permissions.
// securedContainerDataDir must narrow it back down rather than trusting
// whatever mode it already has.
func TestSecuredContainerDataDir_NarrowsPreExistingLaxPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "devsy-data")
	if err := os.Mkdir(dir, 0o777); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}

	got := securedContainerDataDir(dir)
	if got != dir {
		t.Fatalf("securedContainerDataDir() = %q, want %q", got, dir)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat %s: %v", dir, err)
	}
	if perm := info.Mode().Perm(); perm != 0o755 {
		t.Errorf("mode = %o, want 0755 after narrowing", perm)
	}
}

func TestSecuredContainerDataDir_ReturnsEmptyWhenPathIsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	if got := securedContainerDataDir(path); got != "" {
		t.Errorf("securedContainerDataDir() = %q, want empty", got)
	}
}

func TestDirIsWritable_TrueForOwnedDir(t *testing.T) {
	if !dirIsWritable(t.TempDir()) {
		t.Error("expected writable temp dir to report writable")
	}
}

func TestDirIsWritable_FalseForNonexistentDir(t *testing.T) {
	if dirIsWritable(filepath.Join(t.TempDir(), "does-not-exist")) {
		t.Error("expected nonexistent dir to report not writable")
	}
}
