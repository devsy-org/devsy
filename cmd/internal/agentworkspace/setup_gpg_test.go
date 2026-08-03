package agentworkspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquireGPGSetupLock_SucceedsWhenFree(t *testing.T) {
	origPath, origTimeout := gpgSetupLockPath, gpgSetupLockTimeout
	gpgSetupLockPath = filepath.Join(t.TempDir(), "setup-gpg.lock")
	gpgSetupLockTimeout = time.Second
	defer func() { gpgSetupLockPath, gpgSetupLockTimeout = origPath, origTimeout }()

	unlock, err := acquireGPGSetupLock(context.Background())
	require.NoError(t, err)
	unlock()

	reacquire, err := acquireGPGSetupLock(context.Background())
	require.NoError(t, err, "unlock must actually release the lock")
	reacquire()
}

func TestAcquireGPGSetupLock_WaitsForConcurrentHolderThenSucceeds(t *testing.T) {
	origPath, origTimeout := gpgSetupLockPath, gpgSetupLockTimeout
	gpgSetupLockPath = filepath.Join(t.TempDir(), "setup-gpg.lock")
	gpgSetupLockTimeout = 2 * time.Second
	defer func() { gpgSetupLockPath, gpgSetupLockTimeout = origPath, origTimeout }()

	holder := flock.New(gpgSetupLockPath)
	locked, err := holder.TryLock()
	require.NoError(t, err)
	require.True(t, locked)

	const releaseDelay = 200 * time.Millisecond
	start := time.Now()
	releasing := make(chan struct{})
	go func() {
		close(releasing)
		time.Sleep(releaseDelay)
		_ = holder.Unlock()
	}()
	<-releasing

	unlock, err := acquireGPGSetupLock(context.Background())
	elapsed := time.Since(start)

	require.NoError(t, err)
	defer unlock()
	assert.GreaterOrEqual(t, elapsed, releaseDelay/2,
		"second acquirer must wait for the first to release, not run concurrently")
}

func TestAcquireGPGSetupLock_TimesOutWhenHeldTooLong(t *testing.T) {
	origPath, origTimeout := gpgSetupLockPath, gpgSetupLockTimeout
	gpgSetupLockPath = filepath.Join(t.TempDir(), "setup-gpg.lock")
	gpgSetupLockTimeout = 300 * time.Millisecond
	defer func() { gpgSetupLockPath, gpgSetupLockTimeout = origPath, origTimeout }()

	holder := flock.New(gpgSetupLockPath)
	locked, err := holder.TryLock()
	require.NoError(t, err)
	require.True(t, locked)
	defer func() { _ = holder.Unlock() }()

	_, err = acquireGPGSetupLock(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timed out waiting")
}

func TestAcquireGPGSetupLock_ReturnsCancellationErrorWhenCallerCancels(t *testing.T) {
	origPath, origTimeout := gpgSetupLockPath, gpgSetupLockTimeout
	gpgSetupLockPath = filepath.Join(t.TempDir(), "setup-gpg.lock")
	gpgSetupLockTimeout = 10 * time.Second
	defer func() { gpgSetupLockPath, gpgSetupLockTimeout = origPath, origTimeout }()

	holder := flock.New(gpgSetupLockPath)
	locked, err := holder.TryLock()
	require.NoError(t, err)
	require.True(t, locked)
	defer func() { _ = holder.Unlock() }()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	_, err = acquireGPGSetupLock(ctx)
	require.Error(t, err)
	assert.True(
		t,
		errors.Is(err, context.Canceled),
		"caller cancellation must surface as context.Canceled, not a generic lock-timeout error: got %v",
		err,
	)
}

func TestAcquireGPGSetupLock_FileIsWorldLockable(t *testing.T) {
	origPath, origTimeout := gpgSetupLockPath, gpgSetupLockTimeout
	gpgSetupLockPath = filepath.Join(t.TempDir(), "setup-gpg.lock")
	gpgSetupLockTimeout = time.Second
	defer func() { gpgSetupLockPath, gpgSetupLockTimeout = origPath, origTimeout }()

	unlock, err := acquireGPGSetupLock(context.Background())
	require.NoError(t, err)
	unlock()

	info, err := os.Stat(gpgSetupLockPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o666), info.Mode().Perm(),
		"lock file must be 0666 so any container user (root or the workspace's "+
			"remoteUser) can create/open it; the default flock mode of 0600 lets "+
			"whichever user runs setup-gpg first lock out every other user "+
			"with EACCES")
}

// TestAcquireGPGSetupLock_SecondAcquireOfAlready0666FileDoesNotChmod covers
// a non-owning second acquirer of an already-0666 lock, whose chmod would EPERM.
func TestAcquireGPGSetupLock_SecondAcquireOfAlready0666FileDoesNotChmod(t *testing.T) {
	origPath, origTimeout := gpgSetupLockPath, gpgSetupLockTimeout
	gpgSetupLockPath = filepath.Join(t.TempDir(), "setup-gpg.lock")
	gpgSetupLockTimeout = time.Second
	defer func() { gpgSetupLockPath, gpgSetupLockTimeout = origPath, origTimeout }()

	unlock, err := acquireGPGSetupLock(context.Background())
	require.NoError(t, err)
	unlock()

	info, err := os.Stat(gpgSetupLockPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o666), info.Mode().Perm())

	needsChmod, err := lockFileNeedsChmod(gpgSetupLockPath, 0o666)
	require.NoError(t, err)
	assert.False(t, needsChmod,
		"a lock file already at 0666 must not need a chmod, since a "+
			"non-owning second acquirer's chmod would fail with EPERM")

	reacquire, err := acquireGPGSetupLock(context.Background())
	require.NoError(t, err, "second acquisition of an already-0666 lock file must succeed")
	reacquire()
}

// TestAcquireGPGSetupLock_FixesWrongMode ensures the skip-chmod-if-0666
// optimization doesn't skip fixing a genuinely wrong mode.
func TestAcquireGPGSetupLock_FixesWrongMode(t *testing.T) {
	origPath, origTimeout := gpgSetupLockPath, gpgSetupLockTimeout
	gpgSetupLockPath = filepath.Join(t.TempDir(), "setup-gpg.lock")
	gpgSetupLockTimeout = time.Second
	defer func() { gpgSetupLockPath, gpgSetupLockTimeout = origPath, origTimeout }()

	unlock, err := acquireGPGSetupLock(context.Background())
	require.NoError(t, err)
	unlock()

	// #nosec G302 -- intentional: simulating a wrong pre-existing mode
	require.NoError(t, os.Chmod(gpgSetupLockPath, 0o644))

	needsChmod, err := lockFileNeedsChmod(gpgSetupLockPath, 0o666)
	require.NoError(t, err)
	assert.True(t, needsChmod, "a lock file at 0644 must be reported as needing a chmod to 0666")

	reacquire, err := acquireGPGSetupLock(context.Background())
	require.NoError(t, err)
	defer reacquire()

	info, err := os.Stat(gpgSetupLockPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o666), info.Mode().Perm(),
		"acquireGPGSetupLock must fix a wrong mode back to 0666")
}

func TestLockFileNeedsChmod(t *testing.T) {
	tests := []struct {
		name   string
		mode   os.FileMode
		want   os.FileMode
		expect bool
	}{
		{name: "already matches", mode: 0o666, want: 0o666, expect: false},
		{name: "narrower mode needs widening", mode: 0o644, want: 0o666, expect: true},
		{name: "wider mode needs narrowing", mode: 0o777, want: 0o666, expect: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "lock")
			require.NoError(t, os.WriteFile(path, nil, tc.mode))
			require.NoError(t, os.Chmod(path, tc.mode))

			got, err := lockFileNeedsChmod(path, tc.want)
			require.NoError(t, err)
			assert.Equal(t, tc.expect, got)
		})
	}
}

func TestLockFileNeedsChmod_StatErrorPropagates(t *testing.T) {
	_, err := lockFileNeedsChmod(filepath.Join(t.TempDir(), "does-not-exist"), 0o666)
	require.Error(t, err)
}
