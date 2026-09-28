package task

import (
	"errors"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/stretchr/testify/require"
)

const workspaceOne = "ws-1"

const testProcessIdentity = "sess-1"

func createTaskWith(t *testing.T, store *Store, opts CreateOptions) *Task {
	t.Helper()
	tk, err := store.Create(opts)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return tk
}

func queryActive(t *testing.T, store *Store, workspaceID, command string) []*State {
	t.Helper()
	active, err := store.ActiveForWorkspace(workspaceID, command)
	if err != nil {
		t.Fatalf("ActiveForWorkspace: %v", err)
	}
	return active
}

func deadWorkerWithProcess(
	t *testing.T,
	store *Store,
	opts CreateOptions,
	ref command.ProcessRef,
) *Task {
	t.Helper()
	tk := createTaskWith(t, store, opts)
	if err := tk.SetProcess(ref); err != nil {
		t.Fatalf("SetProcess: %v", err)
	}
	if err := tk.HoldWorkerLock(); err != nil {
		t.Fatalf("HoldWorkerLock: %v", err)
	}
	if err := tk.ReleaseWorkerLock(); err != nil {
		t.Fatalf("ReleaseWorkerLock: %v", err)
	}
	return tk
}

func TestReconcileStateMarksCrashedWorkerAbandoned(t *testing.T) {
	store := newTestStore(t)
	tk := deadWorkerWithProcess(
		t,
		store,
		CreateOptions{Command: "up", WorkspaceID: workspaceOne},
		command.ProcessRef{
			PID:      4242,
			TreeKind: command.ProcessTreeUnixGroup,
			Identity: testProcessIdentity,
		},
	)

	state, err := store.Get(tk.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if reconciled := store.ReconcileState(state); !reconciled.Status.Terminal() {
		t.Fatalf("ReconcileState status = %q, want terminal", reconciled.Status)
	}

	persisted, err := store.Get(tk.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !persisted.Status.Terminal() {
		t.Fatalf("persisted status = %q, want terminal", persisted.Status)
	}
}

func TestCleanupExitedWorkerTreeTerminatesCrashedWorkerTree(t *testing.T) {
	store := newTestStore(t)
	ref := command.ProcessRef{
		PID:      4242,
		TreeKind: command.ProcessTreeUnixGroup,
		Identity: testProcessIdentity,
	}
	tk := deadWorkerWithProcess(
		t,
		store,
		CreateOptions{Command: "up", WorkspaceID: workspaceOne},
		ref,
	)
	if err := tk.Fail(ErrAbandoned); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	var cleaned []command.ProcessRef
	store.processController = fakeProcessController{
		cleanup: func(got command.ProcessRef) error {
			cleaned = append(cleaned, got)
			return nil
		},
	}
	if err := store.CleanupExitedWorkerTree(tk.ID()); err != nil {
		t.Fatalf("CleanupExitedWorkerTree: %v", err)
	}
	if len(cleaned) != 1 || cleaned[0] != ref {
		t.Fatalf("cleaned = %+v, want the saved reference", cleaned)
	}
	if err := store.CleanupExitedWorkerTree(tk.ID()); err != nil {
		t.Fatalf("repeat CleanupExitedWorkerTree: %v", err)
	}
	if len(cleaned) != 1 {
		t.Fatalf("cleaned = %+v after repeat, want one cleanup", cleaned)
	}
	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	require.True(t, state.ProcessCleanupComplete, "successful cleanup was not persisted")
}

func TestCleanupExitedWorkerTreeSkipsLiveWorker(t *testing.T) {
	store := newTestStore(t)
	tk := createTaskWith(t, store, CreateOptions{Command: "up", WorkspaceID: workspaceOne})
	if err := tk.SetProcess(
		command.ProcessRef{
			PID:      4242,
			TreeKind: command.ProcessTreeUnixGroup,
			Identity: testProcessIdentity,
		},
	); err != nil {
		t.Fatalf("SetProcess: %v", err)
	}
	if err := tk.HoldWorkerLock(); err != nil {
		t.Fatalf("HoldWorkerLock: %v", err)
	}
	t.Cleanup(func() { _ = tk.ReleaseWorkerLock() })

	store.processController = fakeProcessController{
		cleanup: func(command.ProcessRef) error {
			t.Error("cleaned up a live worker's tree")
			return nil
		},
	}
	if err := store.CleanupExitedWorkerTree(tk.ID()); err != nil {
		t.Fatalf("CleanupExitedWorkerTree: %v", err)
	}
}

func TestCleanupExitedWorkerTreeDoesNotReportSuccessWhileLockHeld(t *testing.T) {
	store := newTestStore(t)
	tk := createTaskWith(t, store, CreateOptions{Command: "up", WorkspaceID: workspaceOne})
	if err := tk.SetProcess(command.ProcessRef{
		PID: 4242, TreeKind: command.ProcessTreeUnixGroup, Identity: testProcessIdentity,
	}); err != nil {
		t.Fatalf("SetProcess: %v", err)
	}
	if err := tk.HoldWorkerLock(); err != nil {
		t.Fatalf("HoldWorkerLock: %v", err)
	}
	t.Cleanup(func() { _ = tk.ReleaseWorkerLock() })
	if err := tk.Fail(ErrAbandoned); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if err := store.CleanupExitedWorkerTree(tk.ID()); err == nil {
		t.Fatal("cleanup reported success while worker lock was held")
	}
}

func TestDeleteKeepsAbandonedTaskWhenTreeCleanupFails(t *testing.T) {
	store := newTestStore(t)
	tk := deadWorkerWithProcess(t, store,
		CreateOptions{Command: "up", WorkspaceID: workspaceOne},
		command.ProcessRef{
			PID:      4242,
			TreeKind: command.ProcessTreeUnixGroup,
			Identity: testProcessIdentity,
		},
	)
	require.NoError(t, tk.Fail(ErrAbandoned))
	store.processController = fakeProcessController{
		cleanup: func(command.ProcessRef) error { return errors.New("cleanup failed") },
	}
	for _, force := range []bool{false, true} {
		require.ErrorContains(t, store.Delete(tk.ID(), force), "cleanup failed")
		_, err := store.Get(tk.ID())
		require.NoError(t, err)
	}
}

func TestActiveForWorkspaceFilters(t *testing.T) {
	store := newTestStore(t)

	up := createTaskWith(t, store, CreateOptions{Command: "up", WorkspaceID: workspaceOne})
	_ = createTaskWith(t, store, CreateOptions{Command: "build", WorkspaceID: workspaceOne})
	otherWorkspace := createTaskWith(t, store, CreateOptions{Command: "up", WorkspaceID: "ws-2"})
	terminal := createTaskWith(t, store, CreateOptions{Command: "up", WorkspaceID: workspaceOne})
	if err := terminal.Succeed(nil); err != nil {
		t.Fatalf("Succeed: %v", err)
	}

	active := queryActive(t, store, workspaceOne, "up")
	if len(active) != 1 || active[0].ID != up.ID() {
		t.Errorf("command-filtered match = %+v, want only the live up task", active)
	}
	if all := queryActive(t, store, workspaceOne, ""); len(all) != 2 {
		t.Errorf("unfiltered match = %d, want 2", len(all))
	}
	if none := queryActive(t, store, otherWorkspace.ID(), "up"); len(none) != 0 {
		t.Errorf("task id used as workspace id matched %d tasks, want 0", len(none))
	}
}

func TestActiveForWorkspaceOrdersNewestFirst(t *testing.T) {
	store := newTestStore(t)

	older := createTaskWith(t, store, CreateOptions{Command: "up", WorkspaceID: workspaceOne})
	if err := store.update(older.ID(), func(s *State) {
		s.StartedAt = s.StartedAt.Add(-time.Hour)
	}); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	newer := createTaskWith(t, store, CreateOptions{Command: "up", WorkspaceID: workspaceOne})

	active, err := store.ActiveForWorkspace(workspaceOne, "up")
	if err != nil {
		t.Fatalf("ActiveForWorkspace: %v", err)
	}
	if len(active) != 2 || active[0].ID != newer.ID() || active[1].ID != older.ID() {
		t.Errorf("unexpected order/contents: %+v", active)
	}
}

func TestActiveForWorkspaceDoesNotReconcileAbandonedWorkers(t *testing.T) {
	store := newTestStore(t)
	tk := createTaskWith(t, store, CreateOptions{Command: "up", WorkspaceID: workspaceOne})

	// A worker that claimed then lost its lock died without recording a
	// result; the query must reconcile it instead of reporting it active.
	if err := tk.HoldWorkerLock(); err != nil {
		t.Fatalf("HoldWorkerLock: %v", err)
	}
	if err := tk.ReleaseWorkerLock(); err != nil {
		t.Fatalf("ReleaseWorkerLock: %v", err)
	}

	active, err := store.ActiveForWorkspace(workspaceOne, "up")
	if err != nil {
		t.Fatalf("ActiveForWorkspace: %v", err)
	}
	if len(active) != 1 || active[0].ID != tk.ID() {
		t.Errorf("query omitted unreconciled task: %+v", active)
	}

	state, err := store.Get(tk.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if state.Status.Terminal() {
		t.Errorf("query changed task status to %q", state.Status)
	}
}

func TestActiveForWorkspaceDoesNotCleanAbandonedProcessTree(t *testing.T) {
	store := newTestStore(t)
	state := abandonTask(t, store)
	if err := store.Open(state.ID).SetWorkspaceID(workspaceOne); err != nil {
		t.Fatalf("SetWorkspaceID: %v", err)
	}
	if err := setPIDForTest(store.Open(state.ID)); err != nil {
		t.Fatalf("SetPID: %v", err)
	}

	killCalls := 0
	store.processController = fakeProcessController{
		terminate: func(command.ProcessRef) error { killCalls++; return nil },
		cleanup:   func(command.ProcessRef) error { killCalls++; return nil },
	}

	active, err := store.ActiveForWorkspace(workspaceOne, "up")
	if err != nil {
		t.Fatalf("ActiveForWorkspace: %v", err)
	}
	if len(active) != 1 || active[0].ID != state.ID {
		t.Fatalf("query omitted an unreconciled task: %+v", active)
	}
	if killCalls != 0 {
		t.Errorf("query invoked process cleanup %d times", killCalls)
	}
}

func TestActiveForWorkspaceDoesNotMutateTaskWhenTreeCleanupWouldFail(t *testing.T) {
	store := newTestStore(t)
	state := abandonTask(t, store)
	if err := store.Open(state.ID).SetWorkspaceID(workspaceOne); err != nil {
		t.Fatalf("SetWorkspaceID: %v", err)
	}
	if err := setPIDForTest(store.Open(state.ID)); err != nil {
		t.Fatalf("SetPID: %v", err)
	}
	store.processController = fakeProcessController{
		terminate: func(command.ProcessRef) error { return errors.New("unexpected terminate") },
		cleanup:   func(command.ProcessRef) error { return errors.New("descendant cleanup failed") },
	}

	active, err := store.ActiveForWorkspace(workspaceOne, "up")
	if err != nil {
		t.Fatalf("ActiveForWorkspace: %v", err)
	}
	if len(active) != 1 || active[0].ID != state.ID {
		t.Fatalf("task was excluded after failed tree cleanup: %+v", active)
	}
	if active[0].Status.Terminal() {
		t.Fatalf("task marked terminal %q after failed tree cleanup", active[0].Status)
	}
}

func TestActiveForWorkspaceTreatsLiveWorkerAsActive(t *testing.T) {
	store := newTestStore(t)
	tk := createTaskWith(t, store, CreateOptions{Command: "up", WorkspaceID: workspaceOne})
	holdWorkerLock(t, tk)

	active, err := store.ActiveForWorkspace(workspaceOne, "up")
	if err != nil {
		t.Fatalf("ActiveForWorkspace: %v", err)
	}
	if len(active) != 1 || active[0].ID != tk.ID() {
		t.Errorf("live worker task not reported active: %+v", active)
	}
}
