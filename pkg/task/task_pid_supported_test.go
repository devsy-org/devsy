//go:build linux || darwin || unix

package task

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestCancelSignalsALiveWorkersPID(t *testing.T) {
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	holdWorkerLock(t, tk)

	worker := exec.Command("sleep", "30")
	worker.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := worker.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	workerExited := make(chan struct{})
	var workerWaitErr error
	go func() {
		workerWaitErr = worker.Wait()
		_ = tk.ReleaseWorkerLock()
		close(workerExited)
	}()
	t.Cleanup(func() {
		_ = worker.Process.Kill()
		<-workerExited
	})
	if err := tk.SetPID(worker.Process.Pid); err != nil {
		t.Fatalf("SetPID: %v", err)
	}

	if err := tk.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	<-workerExited
	if workerWaitErr == nil {
		t.Fatal("worker process exited cleanly, want it signaled by Cancel")
	}
}

func TestCancelUsesPublishedProcessIdentity(t *testing.T) {
	store := newTestStore(t)
	tk, state := newTaskWithWorkerIdentity(t, store)
	if err := tk.HoldWorkerLock(); err != nil {
		t.Fatalf("HoldWorkerLock: %v", err)
	}

	var gotIdentity string
	setKillProcessWithIdentityForTest(store, tk, func(_, _, identity string) error {
		gotIdentity = identity
		return nil
	})
	if err := tk.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if state.ProcessTreeIdentity == "" || gotIdentity != state.ProcessTreeIdentity {
		t.Fatalf(
			"kill identity = %q, want saved identity %q",
			gotIdentity,
			state.ProcessTreeIdentity,
		)
	}
}

func newTaskWithWorkerIdentity(t *testing.T, store *Store) (*Task, *State) {
	t.Helper()
	tk, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	worker := exec.Command("sleep", "30")
	if err := worker.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	t.Cleanup(func() {
		_ = worker.Process.Kill()
		_ = worker.Wait()
	})
	if err := tk.SetPID(worker.Process.Pid); err != nil {
		t.Fatalf("SetPID: %v", err)
	}
	state, err := store.Get(tk.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return tk, state
}

func TestCancelDoesNotSignalAProcessThatReusedThePID(t *testing.T) {
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Claim then release the worker lock: the kernel frees a dead worker's
	// lock the same way, leaving its old PID free for the OS to hand to an
	// unrelated process.
	if err := tk.HoldWorkerLock(); err != nil {
		t.Fatalf("HoldWorkerLock: %v", err)
	}
	if err := tk.ReleaseWorkerLock(); err != nil {
		t.Fatalf("ReleaseWorkerLock: %v", err)
	}

	innocent := exec.Command("sleep", "30")
	if err := innocent.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer func() { _ = innocent.Process.Kill() }()
	if err := tk.SetPID(innocent.Process.Pid); err != nil {
		t.Fatalf("SetPID: %v", err)
	}
	exited := waitAsync(innocent)

	if err := tk.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	assertStillRunning(t, exited)
}

func waitAsync(cmd *exec.Cmd) <-chan error {
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return exited
}

func assertStillRunning(t *testing.T, exited <-chan error) {
	t.Helper()
	select {
	case err := <-exited:
		t.Fatalf("process exited/was signaled unexpectedly: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
}
