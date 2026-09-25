package workspace

import (
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTaskStore(t *testing.T) *task.Store {
	t.Helper()
	store, err := task.NewStoreAt(t.TempDir())
	require.NoError(t, err)
	return store
}

func createUpTask(t *testing.T, store *task.Store, workspaceID string) *task.Task {
	t.Helper()
	tk, err := store.Create(task.CreateOptions{Command: "up", WorkspaceID: workspaceID})
	require.NoError(t, err)
	return tk
}

func taskState(t *testing.T, store *task.Store, id string) *task.State {
	t.Helper()
	state, err := store.Get(id)
	require.NoError(t, err)
	return state
}

func TestQuiesceUpTasksCancelsOnlyActiveUpTasksForWorkspace(t *testing.T) {
	store := newTaskStore(t)
	targetA := createUpTask(t, store, "ws-1")
	targetB := createUpTask(t, store, "ws-1")
	terminal := createUpTask(t, store, "ws-1")
	require.NoError(t, terminal.Succeed(nil))
	otherWorkspace := createUpTask(t, store, "ws-2")
	otherCommand, err := store.Create(task.CreateOptions{Command: "build", WorkspaceID: "ws-1"})
	require.NoError(t, err)

	require.NoError(t, QuiesceUpTasks(store, "ws-1"))

	for _, id := range []string{targetA.ID(), targetB.ID()} {
		state := taskState(t, store, id)
		assert.Equal(t, task.StatusFailed, state.Status, "task %s", id)
		assert.Equal(t, task.ErrCanceled.Error(), state.Error, "task %s", id)
	}
	assert.Equal(t, task.StatusSucceeded, taskState(t, store, terminal.ID()).Status)
	assert.Equal(t, task.StatusPending, taskState(t, store, otherWorkspace.ID()).Status)
	assert.Equal(t, task.StatusPending, taskState(t, store, otherCommand.ID()).Status)
}

func TestQuiesceUpTasksAttemptsEveryTaskAndAggregatesFailures(t *testing.T) {
	store := newTaskStore(t)
	failing := createUpTask(t, store, "ws-1")
	succeeding := createUpTask(t, store, "ws-1")

	// Both workers read as live; one kill keeps failing.
	for _, tk := range []*task.Task{failing, succeeding} {
		require.NoError(t, tk.HoldWorkerLock())
		tk := tk
		t.Cleanup(func() { _ = tk.ReleaseWorkerLockForTest() })
	}
	require.NoError(t, failing.SetPID(1111))
	require.NoError(t, succeeding.SetPID(2222))
	store.SetKillProcessForTest(func(pid string) error {
		if pid == "1111" {
			return errors.New("boom")
		}
		return nil
	})

	err := QuiesceUpTasks(store, "ws-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), failing.ID())

	// The failing task stays retryable while the other task was still
	// attempted and canceled.
	assert.False(t, taskState(t, store, failing.ID()).Status.Terminal())
	state := taskState(t, store, succeeding.ID())
	assert.Equal(t, task.StatusFailed, state.Status)
	assert.Equal(t, task.ErrCanceled.Error(), state.Error)
}
