package workspace

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/command"
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
	require.NoError(t, terminal.SetProcess(command.ProcessRef{
		PID: 4242, TreeKind: command.ProcessTreeUnixGroup, TreeID: "4242",
	}))
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
	assert.False(t, taskState(t, store, terminal.ID()).ProcessCleanupComplete)
	assert.Equal(t, task.StatusPending, taskState(t, store, otherWorkspace.ID()).Status)
	assert.Equal(t, task.StatusPending, taskState(t, store, otherCommand.ID()).Status)
}

func TestQuiesceUpTasksAttemptsEveryTaskAndAggregatesFailures(t *testing.T) {
	store := newTaskStore(t)
	failing := createUpTask(t, store, "ws-1")
	succeeding := createUpTask(t, store, "ws-1")

	// Both workers stay active; cancellation of one task fails.
	for _, tk := range []*task.Task{failing, succeeding} {
		require.NoError(t, tk.HoldWorkerLock())
		tk := tk
		t.Cleanup(func() { _ = tk.ReleaseWorkerLock() })
	}
	err := quiesceUpTasks(
		context.Background(),
		store,
		"ws-1",
		func(_ context.Context, id string) error {
			if id == failing.ID() {
				return errors.New("boom")
			}
			return store.Open(id).Fail(task.ErrCanceled)
		},
	)

	require.Error(t, err)
	assert.Contains(t, err.Error(), failing.ID())

	// The failing task stays retryable while the other task was still
	// attempted and canceled.
	assert.False(t, taskState(t, store, failing.ID()).Status.Terminal())
	state := taskState(t, store, succeeding.ID())
	assert.Equal(t, task.StatusFailed, state.Status)
	assert.Equal(t, task.ErrCanceled.Error(), state.Error)
}

func TestQuiesceUpTasksCleansTerminalTaskTreeInsteadOfCancelling(t *testing.T) {
	store := newTaskStore(t)
	crashed := createUpTask(t, store, "ws-1")
	require.NoError(t, crashed.SetProcess(command.ProcessRef{
		PID:      4242,
		TreeKind: command.ProcessTreeUnixGroup,
		TreeID:   "4242",
	}))
	require.NoError(t, crashed.HoldWorkerLock())
	require.NoError(t, crashed.ReleaseWorkerLock())
	require.NoError(t, crashed.Fail(task.ErrAbandoned))

	var cancelled []string
	require.NoError(t, quiesceUpTasks(
		context.Background(),
		store,
		"ws-1",
		func(_ context.Context, id string) error {
			cancelled = append(cancelled, id)
			return nil
		},
	))

	assert.Empty(t, cancelled, "a terminal task must not be cancelled again")
}

func TestQuiesceUpTasksCleansWorkerThatBecomesAbandonedDuringCancel(t *testing.T) {
	store := newTaskStore(t)
	tk := createUpTask(t, store, "ws-1")
	ref := command.ProcessRef{PID: 99999999, TreeKind: command.ProcessTreeUnixGroup}
	if runtime.GOOS == "windows" {
		ref.TreeKind = command.ProcessTreeWindowsJob
		ref.TreeID = "devsy-test-missing-job"
	}
	require.NoError(t, tk.SetProcess(ref))
	require.NoError(t, tk.HoldWorkerLock())
	require.NoError(t, tk.ReleaseWorkerLock())
	require.NoError(t, quiesceUpTasks(
		context.Background(), store, "ws-1",
		func(context.Context, string) error { return tk.Fail(task.ErrAbandoned) },
	))
	assert.True(t, taskState(t, store, tk.ID()).ProcessCleanupComplete)
}

func TestQuiesceUpTasksBoundsEachCancellation(t *testing.T) {
	store := newTaskStore(t)
	createUpTask(t, store, "ws-1")

	err := quiesceUpTasks(
		context.Background(),
		store,
		"ws-1",
		func(ctx context.Context, _ string) error {
			if _, ok := ctx.Deadline(); !ok {
				return errors.New("task cancellation has no deadline")
			}
			return nil
		},
	)
	require.NoError(t, err)
}
