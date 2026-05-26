//go:build !windows

package opener

import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// spawnSleepHelper starts a long-running child process and registers cleanup
// that kills + reaps it. Returns the spawned cmd.
func spawnSleepHelper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd
}

// waitForDead polls until the given PID is no longer alive or the timeout
// elapses. Returns true if the PID is dead.
func waitForDead(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return syscall.Kill(pid, 0) != nil
}

func TestHelperMatchesState_NilState(t *testing.T) {
	if helperMatchesState(nil) {
		t.Error("expected helperMatchesState(nil) = false")
	}
}

func TestHelperMatchesState_ZeroPID(t *testing.T) {
	if helperMatchesState(&TunnelState{PID: 0}) {
		t.Error("expected helperMatchesState(PID=0) = false")
	}
}

func TestHelperMatchesState_LiveMatch(t *testing.T) {
	cmd := spawnSleepHelper(t)
	pid := cmd.Process.Pid

	ct, err := helperCreateTime(pid)
	if err != nil {
		t.Fatalf("helperCreateTime: %v", err)
	}
	state := &TunnelState{PID: pid, CreateTime: ct}
	if !helperMatchesState(state) {
		t.Errorf(
			"expected helperMatchesState to be true for live child PID %d with matching CreateTime",
			pid,
		)
	}
}

func TestHelperMatchesState_LiveMismatch(t *testing.T) {
	cmd := spawnSleepHelper(t)
	pid := cmd.Process.Pid

	ct, err := helperCreateTime(pid)
	if err != nil {
		t.Fatalf("helperCreateTime: %v", err)
	}
	state := &TunnelState{PID: pid, CreateTime: ct + 1_000_000}
	if helperMatchesState(state) {
		t.Errorf(
			"expected helperMatchesState to be false for live child PID %d with mismatched CreateTime",
			pid,
		)
	}
}

func TestHelperMatchesState_DeadProcess(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	ct, err := helperCreateTime(pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		t.Fatalf("helperCreateTime: %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill child: %v", err)
	}
	if _, err := cmd.Process.Wait(); err != nil {
		// expected: signal: killed
		_ = err
	}
	if !waitForDead(pid, 2*time.Second) {
		t.Fatalf("child PID %d still alive after Kill+Wait", pid)
	}

	state := &TunnelState{PID: pid, CreateTime: ct}
	if helperMatchesState(state) {
		t.Errorf("expected helperMatchesState to be false for dead PID %d", pid)
	}
}

func TestLoadLiveTunnelState_Missing(t *testing.T) {
	setupTempHome(t)
	statePath, err := TunnelStateFilePath("ctx", "ws")
	if err != nil {
		t.Fatalf("TunnelStateFilePath: %v", err)
	}
	got := loadLiveTunnelState("ctx", "ws", statePath)
	if got != nil {
		t.Errorf("expected nil for missing state file, got %+v", *got)
	}
}

func TestLoadLiveTunnelState_Corrupt(t *testing.T) {
	setupTempHome(t)
	statePath, err := TunnelStateFilePath("ctx", "ws")
	if err != nil {
		t.Fatalf("TunnelStateFilePath: %v", err)
	}
	// Touch the workspace dir, then write garbage.
	if err := os.MkdirAll(parentDir(statePath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(statePath, []byte("not json{"), 0o600); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	got := loadLiveTunnelState("ctx", "ws", statePath)
	if got != nil {
		t.Errorf("expected nil for corrupt state, got %+v", *got)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("expected corrupt state file removed; stat err=%v", err)
	}
}

func TestLoadLiveTunnelState_NonMatchingPID(t *testing.T) {
	setupTempHome(t)
	statePath, err := TunnelStateFilePath("ctx", "ws")
	if err != nil {
		t.Fatalf("TunnelStateFilePath: %v", err)
	}
	// PID 1 exists but is not our spawned child; record a deliberately
	// wrong CreateTime so the identity check fails even on systems where
	// gopsutil can read init's CreateTime.
	state := TunnelState{
		PID:        1,
		CreateTime: 1,
		TargetURL:  "http://x",
		Label:      LabelVSCodeBrowser,
	}
	if err := WriteTunnelState("ctx", "ws", state); err != nil {
		t.Fatalf("WriteTunnelState: %v", err)
	}

	got := loadLiveTunnelState("ctx", "ws", statePath)
	if got != nil {
		t.Errorf("expected nil for non-matching PID, got %+v", *got)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Errorf("expected stale state file removed; stat err=%v", err)
	}
}

func TestLoadLiveTunnelState_LiveMatch(t *testing.T) {
	setupTempHome(t)
	cmd := spawnSleepHelper(t)
	pid := cmd.Process.Pid

	ct, err := helperCreateTime(pid)
	if err != nil {
		t.Fatalf("helperCreateTime: %v", err)
	}
	want := TunnelState{
		PID:        pid,
		CreateTime: ct,
		TargetURL:  "http://localhost:10800",
		Label:      LabelVSCodeBrowser,
	}
	if err := WriteTunnelState("ctx", "ws", want); err != nil {
		t.Fatalf("WriteTunnelState: %v", err)
	}
	statePath, err := TunnelStateFilePath("ctx", "ws")
	if err != nil {
		t.Fatalf("TunnelStateFilePath: %v", err)
	}

	got := loadLiveTunnelState("ctx", "ws", statePath)
	if got == nil {
		t.Fatal("expected non-nil state for live matching helper")
	}
	if *got != want {
		t.Errorf("loadLiveTunnelState mismatch:\n got=%+v\nwant=%+v", *got, want)
	}
}

// parentDir returns filepath.Dir(p). Defined locally rather than importing
// path/filepath to keep this test file's import surface minimal.
func parentDir(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == os.PathSeparator {
			return p[:i]
		}
	}
	return "."
}
