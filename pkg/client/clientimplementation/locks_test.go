package clientimplementation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/gofrs/flock"
	"github.com/stretchr/testify/require"
)

const (
	testLocksContext    = "default"
	lockTestWorkspaceID = "ws1"
)

func useTempDevsyHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv(config.EnvHome, home)
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
}

func testLocksDir(t *testing.T) string {
	t.Helper()
	useTempDevsyHome(t, t.TempDir())

	dir, err := provider.GetLocksDir(testLocksContext)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o750))

	return dir
}

func testLockIDs() (string, string, string) {
	return testLocksContext, lockTestWorkspaceID, ""
}

func TestClientLocksLockUnlockRoundTrip(t *testing.T) {
	dir := testLocksDir(t)
	workspaceLock := filepath.Join(dir, lockTestWorkspaceID+".workspace.lock")

	locks := newClientLocks(testLockIDs)
	require.NoError(t, locks.Lock(context.Background()))

	probe := flock.New(workspaceLock)
	locked, err := probe.TryLock()
	require.NoError(t, err)
	require.False(t, locked, "workspace lock should be held after Lock")

	locks.Unlock()

	locked, err = probe.TryLock()
	require.NoError(t, err)
	require.True(t, locked, "workspace lock should be released after Unlock")
	require.NoError(t, probe.Unlock())
}

func TestClientLocksContendedWorkspaceFailsWhenContextCancelled(t *testing.T) {
	dir := testLocksDir(t)

	holder := flock.New(filepath.Join(dir, lockTestWorkspaceID+".workspace.lock"))
	locked, err := holder.TryLock()
	require.NoError(t, err)
	require.True(t, locked)
	t.Cleanup(func() { require.NoError(t, holder.Unlock()) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	locks := newClientLocks(testLockIDs)
	err = locks.Lock(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorContains(t, err, "lock workspace:")
}

func TestClientLocksInitFailureIsCachedAndUnlockDoesNotPanic(t *testing.T) {
	// A regular file as DEVSY_HOME makes creating the locks dir fail.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o600))
	useTempDevsyHome(t, blocker)

	locks := newClientLocks(testLockIDs)

	err := locks.Lock(context.Background())
	require.ErrorContains(t, err, "init lock:")
	require.NotPanics(t, locks.Unlock)

	// Once init has failed, a later valid home must not be picked up.
	useTempDevsyHome(t, t.TempDir())
	err = locks.Lock(context.Background())
	require.ErrorContains(t, err, "init lock:")
}

func TestClientLocksMachineLock(t *testing.T) {
	t.Run("no machine id creates no machine lock", func(t *testing.T) {
		dir := testLocksDir(t)

		locks := newClientLocks(testLockIDs)
		require.NoError(t, locks.Lock(context.Background()))
		locks.Unlock()

		_, err := os.Stat(filepath.Join(dir, ".machine.lock"))
		require.ErrorIs(t, err, os.ErrNotExist)
	})

	t.Run("machine id read lazily on first lock", func(t *testing.T) {
		dir := testLocksDir(t)
		machineID := ""
		locks := newClientLocks(func() (string, string, string) {
			return testLocksContext, lockTestWorkspaceID, machineID
		})

		machineID = "m1"
		require.NoError(t, locks.Lock(context.Background()))

		machineLock := filepath.Join(dir, "m1.machine.lock")
		probe := flock.New(machineLock)
		locked, err := probe.TryLock()
		require.NoError(t, err)
		require.False(t, locked, "machine lock should be held after Lock")

		locks.Unlock()

		locked, err = probe.TryLock()
		require.NoError(t, err)
		require.True(t, locked, "machine lock should be released after Unlock")
		require.NoError(t, probe.Unlock())
	})

	t.Run("machine lock failure releases workspace lock", func(t *testing.T) {
		dir := testLocksDir(t)

		holder := flock.New(filepath.Join(dir, "m1.machine.lock"))
		locked, err := holder.TryLock()
		require.NoError(t, err)
		require.True(t, locked)
		t.Cleanup(func() { require.NoError(t, holder.Unlock()) })

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		locks := newClientLocks(func() (string, string, string) {
			return testLocksContext, lockTestWorkspaceID, "m1"
		})
		err = locks.Lock(ctx)
		require.ErrorContains(t, err, "lock machine:")

		probe := flock.New(filepath.Join(dir, lockTestWorkspaceID+".workspace.lock"))
		locked, err = probe.TryLock()
		require.NoError(t, err)
		require.True(t, locked, "workspace lock must be released after machine lock failure")
		require.NoError(t, probe.Unlock())
	})
}
