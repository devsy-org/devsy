package server

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const osWindows = "windows"

func TestSetupConnectionAgentListener_HappyPath(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("unix socket based test")
	}

	l, socketDir, err := setupConnectionAgentListener("testconn01")
	require.NoError(t, err)
	require.NotNil(t, l)
	t.Cleanup(func() {
		_ = l.Close()
		cleanupAgentSocketDir(socketDir)
	})

	addr := l.Addr().String()
	assert.NotEmpty(t, addr, "listener should have an address")
	assert.True(
		t,
		strings.HasPrefix(addr, socketDir+string(os.PathSeparator)) ||
			filepath.Dir(addr) == socketDir,
		"socket %q should live under socketDir %q",
		addr,
		socketDir,
	)

	info, statErr := os.Stat(socketDir)
	require.NoError(t, statErr)
	assert.True(t, info.IsDir(), "socketDir should be a directory")

	assert.NoError(t, l.Close())
}

func TestCleanupAgentSocketDir(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("unix socket based test")
	}

	t.Run("removes_existing_dir", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "cleanup-target")
		require.NoError(t, os.MkdirAll(dir, 0o750))

		// Drop a file inside to make sure RemoveAll actually clears it.
		require.NoError(t, os.WriteFile(filepath.Join(dir, "sock"), []byte("x"), 0o600))

		cleanupAgentSocketDir(dir)

		_, err := os.Stat(dir)
		assert.True(t, os.IsNotExist(err), "directory should be gone, stat err=%v", err)
	})

	t.Run("empty_path_is_noop", func(t *testing.T) {
		// Must not panic.
		cleanupAgentSocketDir("")
	})

	t.Run("nonexistent_path_is_noop", func(t *testing.T) {
		// Must not panic / explode; os.RemoveAll returns nil for nonexistent.
		cleanupAgentSocketDir(filepath.Join(t.TempDir(), "does-not-exist"))
	})
}

func TestSetupConnectionAgentListener_BadRuntimeDir(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("unix socket based test")
	}

	// linuxPathManager.RuntimeDir derives from os.TempDir(), which honors
	// TMPDIR. Point it under a non-directory path so MkdirAll fails. On
	// darwin, os.TempDir() may not honor TMPDIR in the same way; in that
	// case the test still exercises a read-only parent and should fail.
	//
	// /dev/null is a character device, so attempting to create a directory
	// underneath it will always fail with ENOTDIR.
	t.Setenv("TMPDIR", "/dev/null/definitely-not-a-dir")

	l, socketDir, err := setupConnectionAgentListener("badruntime")
	if err == nil {
		// Some platforms (notably darwin) ignore TMPDIR for the
		// confstr-derived temp dir. Clean up and skip.
		_ = l.Close()
		cleanupAgentSocketDir(socketDir)
		t.Skip("platform does not honor TMPDIR for temp dir resolution")
	}
	assert.Nil(t, l)
	assert.Empty(t, socketDir)
}

func TestNewConnAgentState_LifecycleAndSockPath(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("unix socket based test")
	}

	state, err := newConnAgentState("lifecycle01")
	require.NoError(t, err)
	require.NotNil(t, state)

	assert.Equal(t, state.listener.Addr().String(), state.sockPath(),
		"sockPath must mirror listener.Addr()")
	assert.NotEmpty(t, state.socketDir)

	state.close()
	_, statErr := os.Stat(state.socketDir)
	assert.True(
		t,
		os.IsNotExist(statErr),
		"socketDir should be gone after close, stat err=%v",
		statErr,
	)

	// close on a nil receiver must be safe.
	var nilState *connAgentState
	nilState.close()
}
