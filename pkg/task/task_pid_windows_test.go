//go:build windows

package task

import (
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
)

// sleepWorker returns a long-running process using only tools a Windows
// runner always has.
func sleepWorker() *exec.Cmd {
	return exec.Command("ping", "-n", "30", "127.0.0.1")
}

func TestCancelSignalsALiveWorkersPID(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	worker := sleepWorker()
	workerName := WorkerProcessName(tk.ID())
	if err := command.StartSupervisedBackground(command.SupervisedStartOptions{
		Name: workerName,
		OnStarted: func(ref command.ProcessRef) error {
			return tk.SetProcess(ref)
		},
	}, func() (*exec.Cmd, error) {
		return worker, nil
	}); err != nil {
		t.Fatalf("start supervised worker: %v", err)
	}
	pid := worker.Process.Pid
	t.Cleanup(func() { _ = command.KillTree(strconv.Itoa(pid), workerName) })

	if err := tk.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	running, err := command.IsRunning(strconv.Itoa(pid))
	if err != nil {
		t.Fatalf("IsRunning: %v", err)
	}
	if running {
		t.Fatalf("worker process %d is still running after Cancel", pid)
	}
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

	innocent := sleepWorker()
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
		t.Fatalf("process exited unexpectedly: %v", err)
	case <-time.After(500 * time.Millisecond):
	}
}
