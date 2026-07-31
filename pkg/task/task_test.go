package task

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/status"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStoreAt(t.TempDir())
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	return store
}

func TestGetRejectsPathTraversal(t *testing.T) {
	store := newTestStore(t)
	for _, id := range []string{"../escape", "a/../../b", "/etc/passwd", ".", ".."} {
		if _, err := store.Get(id); err == nil {
			t.Errorf("Get(%q) = nil error, want rejection", id)
		}
	}
}

func TestDeleteRejectsPathTraversal(t *testing.T) {
	store := newTestStore(t)
	if err := store.Delete("../escape", true); err == nil {
		t.Error("Delete with traversal id = nil error, want rejection")
	}
}

func TestCreateStartsPending(t *testing.T) {
	store := newTestStore(t)
	task, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	state, err := store.Get(task.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if state.Status != StatusPending {
		t.Errorf("status = %q, want %q", state.Status, StatusPending)
	}
}

func TestReporterTransitionsToRunning(t *testing.T) {
	store := newTestStore(t)
	task, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	reporter := task.Reporter()
	status.Enter(reporter, status.PhaseBuildingImage, "")

	state, err := store.Get(task.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if state.Status != StatusRunning {
		t.Errorf("status = %q, want %q", state.Status, StatusRunning)
	}
	if state.Phase != string(status.PhaseBuildingImage) {
		t.Errorf("phase = %q, want %q", state.Phase, status.PhaseBuildingImage)
	}
}

func TestReporterRecordsFailure(t *testing.T) {
	store := newTestStore(t)
	task, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	status.Fail(task.Reporter(), status.PhaseRunningLifecycleHook, errors.New("boom"))

	state, err := store.Get(task.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if state.Error != "boom" {
		t.Errorf("error = %q, want %q", state.Error, "boom")
	}
}

func TestSucceedRecordsResult(t *testing.T) {
	store := newTestStore(t)
	task, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	result := &config.Result{RecoveryContainer: true}
	if err := task.Succeed(result); err != nil {
		t.Fatalf("Succeed: %v", err)
	}

	state, err := store.Get(task.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if state.Status != StatusSucceeded {
		t.Errorf("status = %q, want %q", state.Status, StatusSucceeded)
	}
	if state.Result == nil || !state.Result.RecoveryContainer {
		t.Errorf("result = %+v, want RecoveryContainer=true", state.Result)
	}
	if !state.Status.Terminal() {
		t.Error("expected Succeeded to be terminal")
	}
}

func TestFailRecordsError(t *testing.T) {
	store := newTestStore(t)
	task, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := task.Fail(errors.New("container build failed")); err != nil {
		t.Fatalf("Fail: %v", err)
	}

	state, err := store.Get(task.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if state.Status != StatusFailed {
		t.Errorf("status = %q, want %q", state.Status, StatusFailed)
	}
	if state.Error != "container build failed" {
		t.Errorf("error = %q", state.Error)
	}
}

func TestListOrdersMostRecentFirst(t *testing.T) {
	store := newTestStore(t)
	first, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Force distinct StartedAt values without relying on real wall-clock gaps.
	if err := store.update(first.ID(), func(s *State) {
		s.StartedAt = s.StartedAt.Add(-time.Hour)
	}); err != nil {
		t.Fatalf("backdate first: %v", err)
	}
	second, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	states, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(states))
	}
	if states[0].ID != second.ID() || states[1].ID != first.ID() {
		t.Errorf("unexpected order: %+v", states)
	}
}

func TestSetPIDPersists(t *testing.T) {
	store := newTestStore(t)
	task, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := task.SetPID(4242); err != nil {
		t.Fatalf("SetPID: %v", err)
	}

	state, err := store.Get(task.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if state.PID != 4242 {
		t.Errorf("PID = %d, want 4242", state.PID)
	}
}

func TestCancelWithoutPIDMarksFailed(t *testing.T) {
	store := newTestStore(t)
	task, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := task.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	state, err := store.Get(task.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if state.Status != StatusFailed {
		t.Errorf("status = %q, want %q", state.Status, StatusFailed)
	}
	if state.Error != ErrCanceled.Error() {
		t.Errorf("error = %q, want %q", state.Error, ErrCanceled.Error())
	}
}

func TestCancelOnTerminalTaskIsNoop(t *testing.T) {
	store := newTestStore(t)
	task, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := task.Succeed(&config.Result{}); err != nil {
		t.Fatalf("Succeed: %v", err)
	}

	if err := task.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	state, err := store.Get(task.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if state.Status != StatusSucceeded {
		t.Errorf("status = %q, want unchanged %q", state.Status, StatusSucceeded)
	}
}

// A canceled task's outcome must survive its worker finishing afterwards:
// the worker doesn't observe the cancel and reports its own result on the way
// out, which would otherwise erase the reason the task actually stopped.
func TestTerminalStateSurvivesLateWorkerReports(t *testing.T) {
	for _, tc := range []struct {
		name string
		late func(*Task) error
	}{
		{"succeed", func(tk *Task) error { return tk.Succeed(&config.Result{}) }},
		{"fail", func(tk *Task) error { return tk.Fail(errors.New("worker exited")) }},
		{"report", func(tk *Task) error {
			tk.Reporter().Report(status.Event{Phase: status.PhaseReady, Started: true})
			return nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			tsk, err := store.Create(CreateOptions{})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			if err := tsk.Cancel(); err != nil {
				t.Fatalf("Cancel: %v", err)
			}

			if err := tc.late(tsk); err != nil {
				t.Fatalf("late report: %v", err)
			}

			state, err := store.Get(tsk.ID())
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if state.Status != StatusFailed {
				t.Errorf("status = %q, want %q", state.Status, StatusFailed)
			}
			if state.Error != ErrCanceled.Error() {
				t.Errorf("error = %q, want %q", state.Error, ErrCanceled.Error())
			}
		})
	}
}

func TestCreateStoresLabels(t *testing.T) {
	store := newTestStore(t)
	tsk, err := store.Create(CreateOptions{Command: "up", WorkspaceID: "my-ws"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	state, err := store.Get(tsk.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if state.Command != "up" || state.WorkspaceID != "my-ws" {
		t.Errorf("unexpected labels: %+v", state)
	}
}

func TestDeleteRequiresTerminalUnlessForced(t *testing.T) {
	store := newTestStore(t)
	tsk, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := store.Delete(tsk.ID(), false); err == nil {
		t.Error("expected Delete to fail on a non-terminal task")
	}
	if err := store.Delete(tsk.ID(), true); err != nil {
		t.Fatalf("force Delete: %v", err)
	}
	if _, err := store.Get(tsk.ID()); err == nil {
		t.Error("expected Get to fail after Delete")
	}
}

func TestDeleteTerminalTask(t *testing.T) {
	store := newTestStore(t)
	tsk, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := tsk.Succeed(&config.Result{}); err != nil {
		t.Fatalf("Succeed: %v", err)
	}

	if err := store.Delete(tsk.ID(), false); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := store.Get(tsk.ID()); err == nil {
		t.Error("expected Get to fail after Delete")
	}
}

// TestConcurrentUpdatesAcrossStoreInstancesAreSerialized simulates two
// separate processes (each with its own Store instance, as they would be in
// practice) racing to update the same task: a worker reporting progress and
// a canceller. Every individual update must be applied atomically — no
// update should be silently lost or the file left corrupt.
func TestConcurrentUpdatesAcrossStoreInstancesAreSerialized(t *testing.T) {
	dir := t.TempDir()
	workerStore, err := NewStoreAt(dir)
	if err != nil {
		t.Fatalf("NewStoreAt: %v", err)
	}
	tsk, err := workerStore.Create(CreateOptions{Command: "up"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const n = 20
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			store, err := NewStoreAt(dir)
			if err != nil {
				t.Errorf("NewStoreAt: %v", err)
				return
			}
			if err := store.update(tsk.ID(), func(s *State) {
				s.Step = fmt.Sprintf("step-%d", i)
			}); err != nil {
				t.Errorf("update: %v", err)
			}
		})
	}
	wg.Wait()

	final, err := workerStore.Get(tsk.ID())
	if err != nil {
		t.Fatalf("Get after concurrent updates: %v", err)
	}
	if final.Step == "" {
		t.Error("expected some update's Step to have won, got empty")
	}
}

func TestConcurrentReportsAreRaceSafe(t *testing.T) {
	store := newTestStore(t)
	task, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	reporter := task.Reporter()
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			status.Enter(reporter, status.PhaseBuildingImage, "")
			status.Leave(reporter, status.PhaseBuildingImage, "")
		})
	}
	wg.Wait()

	if _, err := store.Get(task.ID()); err != nil {
		t.Fatalf("Get after concurrent reports: %v", err)
	}
}

// holdWorkerLock claims the task's worker lock and releases it on cleanup, so
// the fd closes even if an assertion fails (an open file can block TempDir
// removal on some platforms).
func holdWorkerLock(t *testing.T, tk *Task) {
	t.Helper()
	if err := tk.HoldWorkerLock(); err != nil {
		t.Fatalf("HoldWorkerLock: %v", err)
	}
	t.Cleanup(func() {
		if err := tk.ReleaseWorkerLockForTest(); err != nil {
			t.Errorf("ReleaseWorkerLockForTest: %v", err)
		}
	})
}

func TestAbandonedIgnoresTaskWithNoWorkerLock(t *testing.T) {
	// No lock file yet: the worker hasn't claimed the task, not abandoned.
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{Command: "up"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	state, err := store.Get(tk.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if store.Abandoned(state) {
		t.Error("Abandoned(no worker lock) = true, want false")
	}
}

func TestAbandonedIgnoresTerminalTasks(t *testing.T) {
	store := newTestStore(t)
	for _, st := range []Status{StatusSucceeded, StatusFailed} {
		if store.Abandoned(&State{ID: "any", Status: st}) {
			t.Errorf("Abandoned(%s) = true, want false", st)
		}
	}
}

func TestAbandonedTreatsHeldLockAsLive(t *testing.T) {
	// The lock a live worker holds is not acquirable, so the task reads as live.
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{Command: "up"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	holdWorkerLock(t, tk)
	state, err := store.Get(tk.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if store.Abandoned(state) {
		t.Error("Abandoned(held lock) = true, want false")
	}
}

func TestAbandonedDetectsReleasedLock(t *testing.T) {
	// Releasing stands in for the worker dying: the kernel drops the lock the
	// same way, leaving it acquirable while the task is still non-terminal.
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{Command: "up"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := tk.HoldWorkerLock(); err != nil {
		t.Fatalf("HoldWorkerLock: %v", err)
	}
	if err := tk.ReleaseWorkerLockForTest(); err != nil {
		t.Fatalf("ReleaseWorkerLockForTest: %v", err)
	}
	state, err := store.Get(tk.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !store.Abandoned(state) {
		t.Error("Abandoned(released lock) = false, want true")
	}
}

func TestHoldWorkerLockRejectsSecondWorker(t *testing.T) {
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{Command: "up"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	holdWorkerLock(t, tk)

	// A second worker for the same task must not start alongside the first.
	if err := store.Open(tk.ID()).HoldWorkerLock(); err == nil {
		t.Error("second HoldWorkerLock = nil error, want rejection")
	}
}

// abandonTask creates a task, claims its worker lock, then releases it: the
// kernel frees a dead worker's lock the same way.
func abandonTask(t *testing.T, store *Store) *State {
	t.Helper()
	tk, err := store.Create(CreateOptions{Command: "up"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := tk.HoldWorkerLock(); err != nil {
		t.Fatalf("HoldWorkerLock: %v", err)
	}
	if err := tk.ReleaseWorkerLockForTest(); err != nil {
		t.Fatalf("ReleaseWorkerLockForTest: %v", err)
	}
	state, err := store.Get(tk.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return state
}

func TestReconcileFailsAbandonedTask(t *testing.T) {
	store := newTestStore(t)
	state := abandonTask(t, store)

	reconciled := store.Reconcile(state)

	if reconciled.Status != StatusFailed {
		t.Errorf("status = %s, want %s", reconciled.Status, StatusFailed)
	}
	if reconciled.Error != ErrAbandoned.Error() {
		t.Errorf("error = %q, want %q", reconciled.Error, ErrAbandoned.Error())
	}
}

func TestReconcilePersistsTheFailure(t *testing.T) {
	store := newTestStore(t)
	state := abandonTask(t, store)

	store.Reconcile(state)

	persisted, err := store.Get(state.ID)
	if err != nil {
		t.Fatalf("Get after reconcile: %v", err)
	}
	if persisted.Status != StatusFailed {
		t.Errorf("persisted status = %s, want %s", persisted.Status, StatusFailed)
	}
}

func TestReconcileDoesNotFailARestartedWorker(t *testing.T) {
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{Command: "up"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := tk.HoldWorkerLock(); err != nil {
		t.Fatalf("HoldWorkerLock: %v", err)
	}
	if err := tk.ReleaseWorkerLockForTest(); err != nil {
		t.Fatalf("ReleaseWorkerLockForTest: %v", err)
	}
	stale, err := store.Get(tk.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	// A replacement worker takes over mid-reconcile. Holding the lock is what
	// a real worker does; it must not be able to succeed here, because
	// Reconcile still holds it.
	restarted := store.Open(tk.ID())
	claimed := false
	store.SetAfterClaimForTest(func() {
		claimed = restarted.HoldWorkerLock() == nil
	})

	reconciled := store.Reconcile(stale)

	if claimed {
		t.Error("a replacement worker acquired the lock during Reconcile")
		if err := restarted.ReleaseWorkerLockForTest(); err != nil {
			t.Errorf("ReleaseWorkerLockForTest: %v", err)
		}
	}
	// With the lock held throughout, the reconcile is the only writer and the
	// abandoned task is correctly failed.
	if reconciled.Status != StatusFailed {
		t.Errorf("status = %s, want %s", reconciled.Status, StatusFailed)
	}
}

func TestReconcileKeepsAWorkerRecordedResult(t *testing.T) {
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{Command: "up"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := tk.HoldWorkerLock(); err != nil {
		t.Fatalf("HoldWorkerLock: %v", err)
	}
	stale, err := store.Get(tk.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Worker succeeds, then exits (dropping its lock).
	if err := tk.Succeed(nil); err != nil {
		t.Fatalf("Succeed: %v", err)
	}
	if err := tk.ReleaseWorkerLockForTest(); err != nil {
		t.Fatalf("ReleaseWorkerLockForTest: %v", err)
	}

	reconciled := store.Reconcile(stale)

	if reconciled.Status != StatusSucceeded {
		t.Errorf("status = %s, want %s", reconciled.Status, StatusSucceeded)
	}
	if reconciled.Error != "" {
		t.Errorf("error = %q, want empty", reconciled.Error)
	}
}

func TestReconcileLeavesLiveTaskAlone(t *testing.T) {
	store := newTestStore(t)
	task, err := store.Create(CreateOptions{Command: "up"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	holdWorkerLock(t, task)
	state, err := store.Get(task.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got := store.Reconcile(state).Status; got != StatusPending {
		t.Errorf("status = %s, want %s (unchanged)", got, StatusPending)
	}
}

func TestReconcilePreservesCancellationReason(t *testing.T) {
	store := newTestStore(t)
	task, err := store.Create(CreateOptions{Command: "up"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := task.Fail(ErrCanceled); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	state, err := store.Get(task.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got := store.Reconcile(state).Error; got != ErrCanceled.Error() {
		t.Errorf("error = %q, want %q", got, ErrCanceled.Error())
	}
}
