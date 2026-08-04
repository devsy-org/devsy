//go:build linux || darwin || unix

package setup

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestWriteResultFileTo_RejectsSymlinkWithoutFollowing guards against a
// symlink planted at DevContainerResultPath's fixed, predictable path
// redirecting the write onto an arbitrary target — the same class of
// attack pkg/sharedfile's other callers (the GPG lock, the activity file)
// already defend against.
func TestWriteResultFileTo_RejectsSymlinkWithoutFollowing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "result.json")
	decoyTarget := filepath.Join(dir, "decoy")
	if err := os.WriteFile(decoyTarget, []byte("untouched"), 0o600); err != nil {
		t.Fatalf("seed decoy: %v", err)
	}
	if err := os.Symlink(decoyTarget, path); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := writeResultFileTo(path, []byte(`{"ok":true}`)); err == nil {
		t.Fatal("expected writeResultFileTo to reject a symlinked path, got nil error")
	}

	//nolint:gosec // test-owned temp path
	content, err := os.ReadFile(decoyTarget)
	if err != nil {
		t.Fatalf("stat decoy: %v", err)
	}
	if string(content) != "untouched" {
		t.Errorf("decoy content = %q, want unchanged %q", content, "untouched")
	}
}

// TestWriteResultFileTo_RejectsFIFOWithoutBlocking guards against a FIFO
// planted at the result-file path hanging the container's own setup phase
// forever: opening a FIFO for read/write with no matching peer blocks
// indefinitely without O_NONBLOCK.
func TestWriteResultFileTo_RejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	if err := syscall.Mkfifo(path, 0o666); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- writeResultFileTo(path, []byte(`{"ok":true}`)) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected writeResultFileTo to reject a FIFO, got nil error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("writeResultFileTo blocked for 2s+ opening a FIFO with no peer")
	}
}
