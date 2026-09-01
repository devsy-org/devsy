//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecuredContainerDataDir_RejectsSymlink(t *testing.T) {
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o755); err != nil { // #nosec G301 -- test directory
		t.Fatalf("mkdir target: %v", err)
	}
	link := filepath.Join(t.TempDir(), "devsy-data")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink %s -> %s: %v", link, target, err)
	}

	if got := securedContainerDataDir(link); got != "" {
		t.Errorf("securedContainerDataDir() = %q, want empty", got)
	}
}
