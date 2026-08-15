package file

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsLocalDirExistingDirectory(t *testing.T) {
	dir := t.TempDir()
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	isLocal, name := IsLocalDir(dir)
	if !isLocal {
		t.Fatalf("IsLocalDir(%q) = false, want true", dir)
	}
	if name != abs {
		t.Fatalf("name = %q, want %q", name, abs)
	}
}

func TestIsLocalDirExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	isLocal, name := IsLocalDir(path)
	if !isLocal {
		t.Fatalf("IsLocalDir(%q) = false, want true", path)
	}
	if name != abs {
		t.Fatalf("name = %q, want %q", name, abs)
	}
}

func TestIsLocalDirRelativePathResolvesToAbsolute(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	if err := os.Mkdir("relsub", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	isLocal, name := IsLocalDir("relsub")
	if !isLocal {
		t.Fatalf("IsLocalDir(%q) = false, want true", "relsub")
	}
	if !filepath.IsAbs(name) {
		t.Fatalf("name = %q, want an absolute path", name)
	}
	if resolved, err := filepath.Abs("relsub"); err == nil && name != resolved {
		t.Fatalf("name = %q, want %q", name, resolved)
	}
}

func TestIsLocalDirNonExistentPathReturnsNameUnchanged(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	isLocal, name := IsLocalDir(missing)
	if isLocal {
		t.Fatalf("IsLocalDir(%q) = true, want false", missing)
	}
	if name != missing {
		t.Fatalf("name = %q, want %q", name, missing)
	}
}
