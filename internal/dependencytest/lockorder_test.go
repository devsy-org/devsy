package dependencytest

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLockOrderAllowsSelection(t *testing.T) {
	var order LockOrder
	require.NoError(t, order.Check())
	require.NoError(t, order.Acquire(ArtifactLock))
	require.NoError(t, order.Work(NetworkWork))
	require.NoError(t, order.Release(ArtifactLock))
	require.NoError(t, order.Acquire(GateLock))
	require.NoError(t, order.TryInit(true))
	require.NoError(t, order.Acquire(ConfigLock))
	require.NoError(t, order.Work(MetadataWork))
	require.NoError(t, order.Release(ConfigLock))
	require.NoError(t, order.Release(InitLock))
	require.NoError(t, order.Release(GateLock))
	for _, work := range []WorkKind{ProcessWork, NetworkWork, PromptWork, RuntimeWork, WorkspaceWork} {
		require.NoError(t, order.Work(work))
	}
	require.NoError(t, order.Check())
}

func TestLockOrderAllowsRemoval(t *testing.T) {
	var order LockOrder
	require.NoError(t, order.Acquire(GateLock))
	require.NoError(t, order.Acquire(ArtifactLock))
	require.NoError(t, order.Acquire(ConfigLock))
	require.NoError(t, order.Work(MetadataWork))
	require.NoError(t, order.Release(ConfigLock))
	require.NoError(t, order.Release(ArtifactLock))
	require.NoError(t, order.Release(GateLock))
	require.NoError(t, order.Check())
}

func TestLockOrderRejectsAcquisition(t *testing.T) {
	for _, tt := range []struct {
		name string
		held []LockKind
		next LockKind
	}{
		{name: "artifact before G", held: []LockKind{ArtifactLock}, next: GateLock},
		{name: "init before G", held: []LockKind{InitLock}, next: GateLock},
		{name: "config before G", next: ConfigLock},
		{name: "blocking init", next: InitLock},
		{name: "waiting for init under G", held: []LockKind{GateLock}, next: InitLock},
		{name: "waiting for init under config", held: []LockKind{GateLock, ConfigLock}, next: InitLock},
		{name: "duplicate artifact", held: []LockKind{ArtifactLock}, next: ArtifactLock},
		{name: "duplicate G", held: []LockKind{GateLock}, next: GateLock},
		{name: "duplicate config", held: []LockKind{GateLock, ConfigLock}, next: ConfigLock},
		{name: "unknown lock", next: LockKind("unknown")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var order LockOrder
			holdLocks(t, &order, tt.held...)
			require.Error(t, order.Acquire(tt.next))
			for _, lock := range slices.Backward(tt.held) {
				require.NoError(t, order.Release(lock))
			}
			require.NoError(t, order.Check())
		})
	}
}

func TestLockOrderTryInit(t *testing.T) {
	for _, held := range [][]LockKind{nil, {GateLock}, {GateLock, ConfigLock}} {
		var order LockOrder
		holdLocks(t, &order, held...)
		require.NoError(t, order.TryInit(false))
		require.Error(t, order.Release(InitLock))
		require.NoError(t, order.TryInit(true))
		require.Error(t, order.TryInit(true))
		require.Error(t, order.TryInit(false))
		require.NoError(t, order.Release(InitLock))
		for _, lock := range slices.Backward(held) {
			require.NoError(t, order.Release(lock))
		}
		require.NoError(t, order.Check())
	}
	var order LockOrder
	require.NoError(t, order.TryInit(false))
	require.NoError(t, order.Acquire(GateLock))
	require.NoError(t, order.Release(GateLock))
	require.NoError(t, order.TryInit(true))
	require.Error(t, order.Acquire(GateLock))
	require.NoError(t, order.Release(InitLock))
	require.NoError(t, order.Acquire(GateLock))
	require.NoError(t, order.Release(GateLock))
	require.NoError(t, order.Check())
}

func TestLockOrderRequiresReverseRelease(t *testing.T) {
	var order LockOrder
	require.Error(t, order.Release(GateLock))
	holdLocks(t, &order, GateLock, ConfigLock)
	require.Error(t, order.Check())
	require.Error(t, order.Release(GateLock))
	require.NoError(t, order.Release(ConfigLock))
	require.Error(t, order.Check())
	require.NoError(t, order.Release(GateLock))
	require.NoError(t, order.Check())
}

func TestLockOrderRejectsForbiddenWork(t *testing.T) {
	for _, held := range [][]LockKind{{GateLock}, {GateLock, ConfigLock}} {
		var order LockOrder
		holdLocks(t, &order, held...)
		for _, work := range []WorkKind{ProcessWork, NetworkWork, PromptWork, RuntimeWork, WorkspaceWork} {
			require.ErrorContains(t, order.Work(work), "forbidden under G/config")
		}
	}
}

func TestLockOrderRequiresMetadataLocks(t *testing.T) {
	var order LockOrder
	require.Error(t, order.Work(MetadataWork))
	require.NoError(t, order.Acquire(GateLock))
	require.Error(t, order.Work(MetadataWork))
	require.NoError(t, order.Acquire(ConfigLock))
	require.NoError(t, order.Work(MetadataWork))
	require.Error(t, order.Work(WorkKind("unknown")))
}

func holdLocks(t *testing.T, order *LockOrder, locks ...LockKind) {
	t.Helper()
	for _, lock := range locks {
		if lock == InitLock {
			require.NoError(t, order.TryInit(true))
		} else {
			require.NoError(t, order.Acquire(lock))
		}
	}
}
