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

func TestCreateIfNotExistsIsIdempotent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "new")

	if err := CreateIfNotExists(target, 0o750); err != nil {
		t.Fatalf("first CreateIfNotExists: %v", err)
	}
	if !Exists(target) {
		t.Fatalf("expected %s to exist", target)
	}
	if err := CreateIfNotExists(target, 0o750); err != nil {
		t.Fatalf("second CreateIfNotExists on existing dir: %v", err)
	}
}

func TestFileCopiesContentAndMode(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src.txt")
	dst := filepath.Join(root, "dst.txt")

	want := "the quick brown fox"
	if err := os.WriteFile(src, []byte(want), 0o600); err != nil {
		t.Fatalf("write src: %v", err)
	}

	if err := File(src, dst, 0o600); err != nil {
		t.Fatalf("File: %v", err)
	}

	got := mustReadFile(t, dst)
	if string(got) != want {
		t.Fatalf("copied content = %q, want %q", got, want)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("dst mode = %v, want 0o600", info.Mode().Perm())
	}
}

func TestDirectoryCopiesNestedDirAndFile(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")

	nestedFile := filepath.Join(src, "child", "note.txt")
	if err := os.MkdirAll(filepath.Dir(nestedFile), 0o750); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	if err := os.WriteFile(nestedFile, []byte("nested"), 0o600); err != nil {
		t.Fatalf("write nested file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "top.txt"), []byte("top"), 0o600); err != nil {
		t.Fatalf("write top file: %v", err)
	}

	if err := Directory(src, dst); err != nil {
		t.Fatalf("Directory: %v", err)
	}

	if got := mustReadFile(t, filepath.Join(dst, "child", "note.txt")); string(got) != "nested" {
		t.Fatalf("copied nested file = %q, want \"nested\"", got)
	}
	if got := mustReadFile(t, filepath.Join(dst, "top.txt")); string(got) != "top" {
		t.Fatalf("copied top file = %q, want \"top\"", got)
	}
}

func TestDirectoryCopiesSymlink(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")

	if err := os.MkdirAll(src, 0o750); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	linkTarget := "top.txt"
	if err := os.Symlink(linkTarget, filepath.Join(src, "link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := Directory(src, dst); err != nil {
		t.Fatalf("Directory: %v", err)
	}

	link := filepath.Join(dst, "link")
	resolved, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("read copied symlink: %v", err)
	}
	if resolved != linkTarget {
		t.Fatalf("copied symlink target = %q, want %q", resolved, linkTarget)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat copied symlink: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected %s to be a symlink", link)
	}
}

func TestRenameDirectoryMovesTreeAndRemovesSource(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")

	if err := os.MkdirAll(src, 0o750); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatalf("write src file: %v", err)
	}

	if err := RenameDirectory(src, dst); err != nil {
		t.Fatalf("RenameDirectory: %v", err)
	}

	if Exists(src) {
		t.Fatalf("source %s should have been removed", src)
	}
	if got := mustReadFile(t, filepath.Join(dst, "a.txt")); string(got) != "a" {
		t.Fatalf("moved file = %q, want \"a\"", got)
	}
}

const parseUserSpecUser = "alice"

func TestParseUserSpecStripsGroup(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{parseUserSpecUser, parseUserSpecUser},
		{parseUserSpecUser + ":wheel", parseUserSpecUser},
		{parseUserSpecUser + ":wheel:extra", parseUserSpecUser},
		{"", ""},
	}
	for _, c := range cases {
		if got := parseUserSpec(c.in); got != c.want {
			t.Errorf("parseUserSpec(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304 — test temp file
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
