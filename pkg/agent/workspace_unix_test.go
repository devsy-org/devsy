//go:build unix

package agent

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestWipeContentFolderPreservesInode pins the load-bearing invariant of
// wipeContentFolder: the directory's inode must not change. Docker Desktop
// on macOS keys its /host_mnt/... mapping by inode and a recreated
// directory under the same path silently misses the cache.
func TestWipeContentFolderPreservesInode(t *testing.T) {
	dir := t.TempDir()
	content := filepath.Join(dir, "content")
	// #nosec G301 -- test fixture.
	if err := os.MkdirAll(filepath.Join(content, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(content, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(content)
	if err != nil {
		t.Fatal(err)
	}
	beforeIno := st.Sys().(*syscall.Stat_t).Ino

	if err := wipeContentFolder(content); err != nil {
		t.Fatalf("wipeContentFolder: %v", err)
	}

	st, err = os.Stat(content)
	if err != nil {
		t.Fatal(err)
	}
	if afterIno := st.Sys().(*syscall.Stat_t).Ino; afterIno != beforeIno {
		t.Errorf("inode changed: before=%d after=%d (must stay stable)", beforeIno, afterIno)
	}
}
