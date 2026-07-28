package task

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/status"
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
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
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
		}(i)
	}
	wg.Wait()

	// The file must still be valid, readable JSON — not truncated or
	// interleaved — after N concurrent read-modify-writes.
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
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			status.Enter(reporter, status.PhaseBuildingImage, "")
			status.Leave(reporter, status.PhaseBuildingImage, "")
		}()
	}
	wg.Wait()

	if _, err := store.Get(task.ID()); err != nil {
		t.Fatalf("Get after concurrent reports: %v", err)
	}
}
