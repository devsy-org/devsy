//go:build linux || darwin || unix

package command

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

func TestKillTreeTerminatesGroupAfterWorkerExits(t *testing.T) {
	dir := t.TempDir()
	childPIDFile := filepath.Join(dir, "child.pid")
	// #nosec G204 -- the fixed shell script and all paths are test-controlled.
	cmd := exec.Command(
		"sh",
		"-c",
		`sleep 60 >/dev/null 2>&1 & echo $! > "$1"; exit 0`,
		"sh",
		childPIDFile,
	)
	prepareBackgroundTree(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	workerPID := strconv.Itoa(cmd.Process.Pid)
	identity, err := ProcessTreeIdentity(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("process tree identity: %v", err)
	}
	t.Cleanup(func() { _ = killTreeAfterWorkerExit(workerPID, "detached-task", identity) })

	childPID := readTestPID(t, childPIDFile)

	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait for worker exit: %v", err)
	}
	waitForTestProcess(t, childPID, true)

	if err := killTreeAfterWorkerExit(workerPID, "detached-task", identity); err != nil {
		t.Fatalf("killTree after worker exit: %v", err)
	}
	waitForTestProcess(t, childPID, false)
}

func TestKillTreeAfterWorkerExitWithoutIdentityDoesNotSignalUnrelatedGroup(t *testing.T) {
	worker := startUnixTestProcess(t, &syscall.SysProcAttr{Setpgid: true})
	pid := strconv.Itoa(worker.Process.Pid)
	if err := killTreeAfterWorkerExit(pid, "stale-task", ""); err == nil {
		t.Fatal("killTreeAfterWorkerExit without identity = nil for a live process group")
	}
	if running, err := isRunning(pid); err != nil || !running {
		t.Fatalf("unidentified process running=%t, err=%v", running, err)
	}
}

func TestKillTreeAfterWorkerExitWithMismatchedIdentityDoesNotSignalUnrelatedGroup(t *testing.T) {
	worker := startUnixTestProcess(t, &syscall.SysProcAttr{Setpgid: true})
	pid := strconv.Itoa(worker.Process.Pid)

	otherSession := startUnixTestProcess(t, &syscall.SysProcAttr{Setsid: true})
	staleIdentity := processTreeIdentityForTest(t, otherSession.Process.Pid)
	workerIdentity := processTreeIdentityForTest(t, worker.Process.Pid)
	if staleIdentity == workerIdentity {
		t.Fatalf("separate sessions have identical process tree identity %q", workerIdentity)
	}

	if err := killTreeAfterWorkerExit(pid, "stale-task", staleIdentity); err != nil {
		t.Fatalf("killTreeAfterWorkerExit with stale identity: %v", err)
	}
	if running, err := isRunning(pid); err != nil || !running {
		t.Fatalf("unrelated process running=%t, err=%v", running, err)
	}
}

func TestKillTreeWithMismatchedSavedIdentityDoesNotSignalReusedProcessGroup(t *testing.T) {
	worker := startUnixTestProcess(t, &syscall.SysProcAttr{Setpgid: true})
	otherSession := startUnixTestProcess(t, &syscall.SysProcAttr{Setsid: true})
	staleIdentity := processTreeIdentityForTest(t, otherSession.Process.Pid)
	workerIdentity := processTreeIdentityForTest(t, worker.Process.Pid)
	if staleIdentity == workerIdentity {
		t.Fatalf("separate sessions have identical process tree identity %q", workerIdentity)
	}

	if err := killTreeWithIdentity(
		strconv.Itoa(worker.Process.Pid),
		"stale-task",
		staleIdentity,
	); err != nil {
		t.Fatalf("killTreeWithIdentity with stale identity: %v", err)
	}
	if running, err := isRunning(strconv.Itoa(worker.Process.Pid)); err != nil || !running {
		t.Fatalf("reused process running=%t, err=%v", running, err)
	}
}

func TestKillTreeIdentityRejectsDifferentGroupLeaderStartTime(t *testing.T) {
	worker := startUnixTestProcess(t, &syscall.SysProcAttr{Setpgid: true})
	workerIdentity := processTreeIdentityForTest(t, worker.Process.Pid)
	identityParts := strings.Split(workerIdentity, ":")
	if len(identityParts) < 2 {
		t.Fatalf("unexpected process tree identity %q", workerIdentity)
	}
	startTime, err := strconv.ParseInt(identityParts[len(identityParts)-1], 10, 64)
	if err != nil {
		t.Fatalf("parse process start identity: %v", err)
	}
	identityParts[len(identityParts)-1] = strconv.FormatInt(startTime+1, 10)
	staleIdentity := strings.Join(identityParts, ":")

	matches, err := processGroupMatchesIdentity(worker.Process.Pid, staleIdentity)
	if err != nil {
		t.Fatalf("processGroupMatchesIdentity: %v", err)
	}
	if matches {
		t.Fatal("process group matched another process start identity in the same session")
	}
	if err := killTreeWithIdentity(
		strconv.Itoa(worker.Process.Pid),
		"stale-task",
		staleIdentity,
	); err != nil {
		t.Fatalf("killTreeWithIdentity with stale start identity: %v", err)
	}
	if running, err := isRunning(strconv.Itoa(worker.Process.Pid)); err != nil || !running {
		t.Fatalf("worker running=%t, err=%v", running, err)
	}
}

func TestKillTreeWithMissingIdentityDoesNotSignalWorker(t *testing.T) {
	worker := startUnixTestProcess(t, &syscall.SysProcAttr{Setpgid: true})
	pid := strconv.Itoa(worker.Process.Pid)
	if err := killTreeWithIdentity(pid, "task", ""); err == nil {
		t.Fatal("killTreeWithIdentity without saved identity = nil for a live worker")
	}
	if running, err := isRunning(pid); err != nil || !running {
		t.Fatalf("worker running=%t, err=%v", running, err)
	}
}

func TestProcessTreeIdentityMatchesDetachedSession(t *testing.T) {
	worker := exec.Command("sleep", "30")
	worker.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := worker.Start(); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	defer func() { _ = worker.Process.Kill() }()

	identity, err := ProcessTreeIdentity(worker.Process.Pid)
	if err != nil {
		t.Fatalf("process tree identity: %v", err)
	}
	matches, err := processGroupMatchesIdentity(worker.Process.Pid, identity)
	if err != nil {
		t.Fatalf("match process group identity: %v", err)
	}
	if !matches {
		t.Fatal("process group does not belong to its detached session")
	}
}

func startUnixTestProcess(t *testing.T, attr *syscall.SysProcAttr) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = attr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start test process: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

func processTreeIdentityForTest(t *testing.T, pid int) string {
	t.Helper()
	identity, err := ProcessTreeIdentity(pid)
	if err != nil {
		t.Fatalf("read process tree identity: %v", err)
	}
	return identity
}

func readTestPID(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// #nosec G304 -- this test reads only its own temporary PID file.
		data, err := os.ReadFile(path)
		if err == nil {
			return strings.TrimSpace(string(data))
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("worker did not publish PID in %s", path)
	return ""
}

func waitForTestProcess(t *testing.T, pid string, wantRunning bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		running, err := isRunning(pid)
		if err != nil {
			t.Fatalf("isRunning(%s): %v", pid, err)
		}
		if running == wantRunning {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %s running=%t, want %t", pid, !wantRunning, wantRunning)
}

func TestKillInvalidPIDReturnsError(t *testing.T) {
	for _, pid := range []string{"not-a-pid", "0", "-5"} {
		if _, err := IsRunning(pid); err == nil {
			t.Errorf("IsRunning(%q) = nil error, want rejection", pid)
		}
		if err := Kill(pid); err == nil {
			t.Errorf("Kill(%q) = nil, want error", pid)
		}
	}
}
