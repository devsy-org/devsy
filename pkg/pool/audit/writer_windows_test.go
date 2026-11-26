//go:build windows

package audit

import (
	"io/fs"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

const writerWindowsVersion = "sha256:0000000000000000000000000000000000000000000000000000000000000001"

func TestWriterCannotBeBuiltOnWindows(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "audit")
	store, err := Open(dir)
	require.ErrorIs(t, err, errWindowsUnsupported)
	require.Nil(t, store)
	w, err := NewWriter(store, WriterConfig{
		Pool:          "test-pool",
		Host:          "test-host",
		PolicyVersion: writerWindowsVersion,
		ConfigVersion: writerWindowsVersion,
	})
	require.Error(t, err)
	require.Nil(t, w)
	require.NoDirExists(t, dir, "a refused Open must not create the directory")
}

func TestAppendFailureKeepsOnlyTheErrnoOnWindows(t *testing.T) {
	const homeDir = `C:\Users\private-canary\audit`
	cause := &fs.PathError{Op: "open", Path: homeDir, Err: syscall.Errno(3)}
	err := appendFailure("session.request", cause)
	require.ErrorIs(t, err, syscall.Errno(3))
	require.NotContains(t, err.Error(), "private-canary")
	require.NotContains(t, err.Error(), "open ")
}
