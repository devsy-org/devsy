//go:build linux || darwin || unix

package sharedfile

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWidenIfNeeded_RejectsFIFOWithoutBlocking is the regression test for
// CodeRabbit's finding: opening a FIFO planted at path with no writer
// blocks forever without O_NONBLOCK, letting any container user with
// write access to the coordination file's directory hang every future
// caller. WidenIfNeeded must return promptly with an error instead.
func TestWidenIfNeeded_RejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	require.NoError(t, syscall.Mkfifo(path, 0o666))

	done := make(chan error, 1)
	go func() { done <- WidenIfNeeded(path, 0o666) }()

	select {
	case err := <-done:
		require.Error(t, err,
			"a FIFO at the coordination path must be rejected, not silently accepted")
		assert.Contains(t, err.Error(), "not a regular file")
	case <-time.After(2 * time.Second):
		t.Fatal("WidenIfNeeded blocked for 2s+ opening a FIFO with no writer")
	}
}

func TestReadFile_RejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	require.NoError(t, syscall.Mkfifo(path, 0o666))

	done := make(chan error, 1)
	go func() {
		_, err := ReadFile(path)
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("ReadFile blocked for 2s+ opening a FIFO with no writer")
	}
}

func TestWriteFile_RejectsFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "coord")
	require.NoError(t, syscall.Mkfifo(path, 0o666))

	done := make(chan error, 1)
	go func() { done <- WriteFile(path, []byte("x"), 0o644) }()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("WriteFile blocked for 2s+ opening a FIFO with no reader")
	}
}
