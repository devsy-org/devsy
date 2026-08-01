//go:build linux || darwin || unix

package command

import (
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestKillTerminatesRunningProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pid := strconv.Itoa(cmd.Process.Pid)

	if err := Kill(pid); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("process still running after Kill")
	}
}

func TestKillOnAlreadyExitedProcessIsNoop(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	pid := strconv.Itoa(cmd.Process.Pid)

	if err := Kill(pid); err != nil {
		t.Errorf("Kill on exited process = %v, want nil", err)
	}
}

func TestKillInvalidPIDReturnsError(t *testing.T) {
	if err := Kill("not-a-pid"); err == nil {
		t.Error("Kill(\"not-a-pid\") = nil, want error")
	}
}
