package agentworkspace

import (
	"context"
	"errors"
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

	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = holder.Unlock()
	}()

	start := time.Now()
	unlock, err := acquireGPGSetupLock(context.Background())
	elapsed := time.Since(start)

	require.NoError(t, err)
	defer unlock()
	assert.GreaterOrEqual(t, elapsed, 150*time.Millisecond,
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
	// Longer than the caller's cancellation, so a "timed out waiting"
	// lock-timeout error is not what triggers here — only ctx cancellation.
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
	assert.True(t, errors.Is(err, context.Canceled),
		"caller cancellation must surface as context.Canceled, not a generic lock-timeout error: got %v", err)
}
