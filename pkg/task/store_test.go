package task

import (
	"testing"
	"time"
)

func createTaskWith(t *testing.T, store *Store, opts CreateOptions) *Task {
	t.Helper()
	tk, err := store.Create(opts)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return tk
}

func TestActiveForWorkspaceFiltersAndOrders(t *testing.T) {
	store := newTestStore(t)

	older := createTaskWith(t, store, CreateOptions{Command: "up", WorkspaceID: "ws-1"})
	if err := store.update(older.ID(), func(s *State) {
		s.StartedAt = s.StartedAt.Add(-time.Hour)
	}); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	newer := createTaskWith(t, store, CreateOptions{Command: "up", WorkspaceID: "ws-1"})
	otherCommand := createTaskWith(t, store, CreateOptions{Command: "build", WorkspaceID: "ws-1"})
	otherWorkspace := createTaskWith(t, store, CreateOptions{Command: "up", WorkspaceID: "ws-2"})
	terminal := createTaskWith(t, store, CreateOptions{Command: "up", WorkspaceID: "ws-1"})
	if err := terminal.Succeed(nil); err != nil {
		t.Fatalf("Succeed: %v", err)
	}

	active, err := store.ActiveForWorkspace("ws-1", "up")
	if err != nil {
		t.Fatalf("ActiveForWorkspace: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("got %d active tasks, want 2: %+v", len(active), active)
	}
	if active[0].ID != newer.ID() || active[1].ID != older.ID() {
		t.Errorf("unexpected order/contents: %s, %s", active[0].ID, active[1].ID)
	}

	all, err := store.ActiveForWorkspace("ws-1", "")
	if err != nil {
		t.Fatalf("ActiveForWorkspace: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("unfiltered match = %d, want 3 (includes %s)", len(all), otherCommand.ID())
	}

	none, err := store.ActiveForWorkspace(otherWorkspace.ID(), "up")
	if err != nil {
		t.Fatalf("ActiveForWorkspace: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("task id used as workspace id matched %d tasks, want 0", len(none))
	}
}

func TestActiveForWorkspaceReconcilesAbandonedWorkers(t *testing.T) {
	store := newTestStore(t)
	tk := createTaskWith(t, store, CreateOptions{Command: "up", WorkspaceID: "ws-1"})

	// A worker that claimed then lost its lock died without recording a
	// result; the query must reconcile it instead of reporting it active.
	if err := tk.HoldWorkerLock(); err != nil {
		t.Fatalf("HoldWorkerLock: %v", err)
	}
	if err := tk.ReleaseWorkerLockForTest(); err != nil {
		t.Fatalf("ReleaseWorkerLockForTest: %v", err)
	}

	active, err := store.ActiveForWorkspace("ws-1", "up")
	if err != nil {
		t.Fatalf("ActiveForWorkspace: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("abandoned task reported active: %+v", active)
	}

	state, err := store.Get(tk.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !state.Status.Terminal() {
		t.Errorf("reconciled task status = %q, want terminal", state.Status)
	}
}

func TestActiveForWorkspaceTreatsLiveWorkerAsActive(t *testing.T) {
	store := newTestStore(t)
	tk := createTaskWith(t, store, CreateOptions{Command: "up", WorkspaceID: "ws-1"})
	holdWorkerLock(t, tk)

	active, err := store.ActiveForWorkspace("ws-1", "up")
	if err != nil {
		t.Fatalf("ActiveForWorkspace: %v", err)
	}
	if len(active) != 1 || active[0].ID != tk.ID() {
		t.Errorf("live worker task not reported active: %+v", active)
	}
}
