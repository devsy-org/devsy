//go:build linux || darwin || unix

package copy

import (
	"os"
	"os/user"
	"path/filepath"
	"syscall"
	"testing"
)

func currentUserName(t *testing.T) string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatalf("current user: %v", err)
	}
	return u.Username
}

func ownerUID(t *testing.T, path string) uint32 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("no syscall stat for %s", path)
	}
	return stat.Uid
}

func TestMkdirAllChownCreatesAndOwnsNewDirs(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "a", "b", "c")

	if err := MkdirAllChown(target, 0o755, currentUserName(t)); err != nil {
		t.Fatalf("MkdirAllChown: %v", err)
	}

	self := os.Getuid()
	for _, dir := range []string{
		filepath.Join(root, "a"),
		filepath.Join(root, "a", "b"),
		target,
	} {
		if got := int(ownerUID(t, dir)); got != self {
			t.Errorf("dir %s owned by uid %d, want %d", dir, got, self)
		}
	}
}

func TestMkdirAllChownIdempotentOnExisting(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "x", "y")

	if err := MkdirAllChown(target, 0o755, currentUserName(t)); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := MkdirAllChown(target, 0o755, currentUserName(t)); err != nil {
		t.Fatalf("second call on existing path: %v", err)
	}
}

func TestMkdirAllChownEmptyUserSkipsChown(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "p", "q")

	if err := MkdirAllChown(target, 0o755, ""); err != nil {
		t.Fatalf("MkdirAllChown empty user: %v", err)
	}
	if !Exists(target) {
		t.Fatalf("expected %s to be created", target)
	}
}
