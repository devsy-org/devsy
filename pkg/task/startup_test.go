package task

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/stretchr/testify/require"
)

func TestReconcileWorkerBeforeClaim(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unclaimed", true: "claimed"}[claimed], func(t *testing.T) {
			store := newTestStore(t)
			store.processController = fakeProcessController{}
			tk := createTaskWith(t, store, CreateOptions{Command: "up"})
			child := startupTestChild()
			require.NoError(t, child.Start())
			t.Cleanup(func() { _ = child.Process.Kill() })
			require.NoError(t, tk.SetPID(child.Process.Pid))
			if claimed {
				require.NoError(t, tk.HoldWorkerLock())
			}
			state, err := store.ReconcileTask(context.Background(), tk.ID())
			require.NoError(t, err)
			require.False(t, state.Status.Terminal(), "healthy startup must not be abandoned")
			require.NoError(t, child.Process.Kill())
			_ = child.Wait()
			if claimed {
				require.NoError(t, tk.ReleaseWorkerLock())
			}
			var wg sync.WaitGroup
			results := make(chan error, 4)
			for range 4 {
				wg.Go(
					func() { _, err := store.ReconcileTask(context.Background(), tk.ID()); results <- err },
				)
			}
			wg.Wait()
			close(results)
			for err := range results {
				require.NoError(t, err)
			}
			state, err = store.ReconcileTask(context.Background(), tk.ID())
			require.NoError(t, err)
			require.Equal(t, StatusFailed, state.Status)
			require.True(t, state.ProcessCleanupComplete)
		})
	}
}

func TestReconcileUnclaimedWorkerCleanupRetry(t *testing.T) {
	store := newTestStore(t)
	tk := createTaskWith(t, store, CreateOptions{Command: "up"})
	child := startupTestChild()
	require.NoError(t, child.Start())
	require.NoError(t, tk.SetPID(child.Process.Pid))
	require.NoError(t, child.Process.Kill())
	_ = child.Wait()
	calls := 0
	store.processController = fakeProcessController{cleanup: func(command.ProcessRef) error {
		calls++
		if calls == 1 {
			return errors.New("cleanup unavailable")
		}
		return nil
	}}
	_, err := store.ReconcileTask(context.Background(), tk.ID())
	require.Error(t, err)
	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	require.False(t, state.Status.Terminal())
	state, err = store.ReconcileTask(context.Background(), tk.ID())
	require.NoError(t, err)
	require.Equal(t, StatusFailed, state.Status)
	require.True(t, state.ProcessCleanupComplete)
}

func TestExitConfirmationNeverCreatesWorkerClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "worker.lock")
	calls := 0
	lock, ok := claimConfirmedWorkerExit(path+".reconcile", func() bool {
		calls++
		return calls == 1
	})
	require.False(t, ok)
	require.Nil(t, lock)
	_, err := os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
	lock, ok = claimConfirmedWorkerExit(path+".reconcile", func() bool { return false })
	require.False(t, ok)
	require.Nil(t, lock)
}

func TestUnclaimedWorkerExitPreservesCancellation(t *testing.T) {
	store := newTestStore(t)
	store.processController = fakeProcessController{}
	tk := createTaskWith(t, store, CreateOptions{Command: "up"})
	child := startupTestChild()
	require.NoError(t, child.Start())
	t.Cleanup(func() { _ = child.Process.Kill() })
	require.NoError(t, tk.SetPID(child.Process.Pid))
	_, err := tk.requestCancel()
	require.NoError(t, err)
	require.NoError(t, child.Process.Kill())
	_ = child.Wait()
	state, err := store.ReconcileTask(context.Background(), tk.ID())
	require.NoError(t, err)
	require.True(t, state.Canceled())
	require.True(t, state.ProcessCleanupComplete)
	state, err = store.ReconcileTask(context.Background(), tk.ID())
	require.NoError(t, err)
	require.True(t, state.Canceled())
}
