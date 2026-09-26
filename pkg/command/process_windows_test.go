//go:build windows

package command

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	helperEnvMarker         = "DEVSY_TEST_HELPER_PROCESS"
	helperEnvPIDFile        = "DEVSY_TEST_HELPER_PIDFILE"
	helperEnvChildPIDFile   = "DEVSY_TEST_HELPER_CHILD_PIDFILE"
	helperEnvWaitFile       = "DEVSY_TEST_HELPER_WAIT_FILE"
	helperEnvExitAfterSpawn = "DEVSY_TEST_HELPER_EXIT_AFTER_SPAWN"
)

// TestHelperProcess is not a test; every helper process re-executes the test
// binary into it.
func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperEnvMarker) != "1" {
		return
	}
	if waitFile := os.Getenv(helperEnvWaitFile); waitFile != "" {
		for {
			if _, err := os.Stat(waitFile); err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if pidFile := os.Getenv(helperEnvPIDFile); pidFile != "" {
		child := exec.Command(os.Args[0], "-test.run", "TestHelperProcess")
		child.Env = helperEnv(helperEnvMarker + "=1")
		if grandchildPIDFile := os.Getenv(helperEnvChildPIDFile); grandchildPIDFile != "" {
			child.Env = append(child.Env,
				helperEnvPIDFile+"="+grandchildPIDFile,
				helperEnvExitAfterSpawn+"=1",
			)
		}
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
		if err := os.WriteFile(
			pidFile,
			[]byte(strconv.Itoa(child.Process.Pid)),
			0o600,
		); err != nil {
			os.Exit(1)
		}
		if os.Getenv(helperEnvExitAfterSpawn) == "1" {
			os.Exit(0)
		}
	}
	for {
		time.Sleep(time.Hour)
	}
}

// helperEnv strips helper markers from the inherited environment; Windows
// process creation needs the rest (e.g. SYSTEMROOT) intact.
func helperEnv(extra ...string) []string {
	env := []string{}
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "DEVSY_TEST_HELPER_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, extra...)
}

func startHelper(t *testing.T, extraEnv ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "TestHelperProcess")
	cmd.Env = helperEnv(append([]string{helperEnvMarker + "=1"}, extraEnv...)...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start helper: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

func assertRunning(t *testing.T, pid int) {
	t.Helper()
	running, err := isRunning(strconv.Itoa(pid))
	if err != nil {
		t.Fatalf("isRunning(%d): %v", pid, err)
	}
	if !running {
		t.Fatalf("isRunning(%d) = false, want true", pid)
	}
}

func assertNotRunningEventually(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		running, err := isRunning(strconv.Itoa(pid))
		if err == nil && !running {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("process %d still running after 15s", pid)
}

func TestIsRunningDetectsLiveAndExitedProcess(t *testing.T) {
	cmd := startHelper(t)
	assertRunning(t, cmd.Process.Pid)

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	_ = cmd.Wait()
	assertNotRunningEventually(t, cmd.Process.Pid)
}

func waitForPIDFile(t *testing.T, pidFile string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile) // #nosec G304 -- test-controlled path
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no PID reported in %s", pidFile)
	return 0
}

func TestKillTerminatesProcessTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	parent := startHelper(t, helperEnvPIDFile+"="+pidFile)

	childPID := waitForPIDFile(t, pidFile)

	assertRunning(t, parent.Process.Pid)
	assertRunning(t, childPID)

	if err := Kill(strconv.Itoa(parent.Process.Pid)); err != nil {
		t.Fatalf("Kill(parent): %v", err)
	}

	assertNotRunningEventually(t, parent.Process.Pid)
	assertNotRunningEventually(t, childPID)
}

func TestKillOnAlreadyExitedProcessIsNoop(t *testing.T) {
	cmd := startHelper(t)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill helper: %v", err)
	}
	_ = cmd.Wait()
	assertNotRunningEventually(t, cmd.Process.Pid)

	if err := Kill(strconv.Itoa(cmd.Process.Pid)); err != nil {
		t.Errorf("Kill on exited process = %v, want nil", err)
	}
}

func TestKillIsIdempotent(t *testing.T) {
	cmd := startHelper(t)
	pid := strconv.Itoa(cmd.Process.Pid)

	if err := Kill(pid); err != nil {
		t.Fatalf("first Kill: %v", err)
	}
	if err := Kill(pid); err != nil {
		t.Errorf("second Kill = %v, want nil", err)
	}
}

func TestInvalidPIDReturnsError(t *testing.T) {
	for _, pid := range []string{"not-a-pid", "0", "-5"} {
		if _, err := isRunning(pid); err == nil {
			t.Errorf("isRunning(%q) = nil error, want rejection", pid)
		}
		if err := killTree(pid, ""); err == nil {
			t.Errorf("kill(%q) = nil, want error", pid)
		}
	}
}

func TestKillTerminatesOrphanedGrandchildViaJobObject(t *testing.T) {
	dir := t.TempDir()
	waitFile := filepath.Join(dir, "go")
	childPIDFile := filepath.Join(dir, "child.pid")
	grandchildPIDFile := filepath.Join(dir, "grandchild.pid")

	// The child spawns the grandchild and exits, orphaning the grandchild
	// from the perspective of parent-based termination.
	parent := startHelper(t,
		helperEnvWaitFile+"="+waitFile,
		helperEnvPIDFile+"="+childPIDFile,
		helperEnvChildPIDFile+"="+grandchildPIDFile,
	)
	if err := ownProcessTree(parent.Process.Pid, "devsy-test-worker"); err != nil {
		t.Skipf("job assignment unavailable in this environment: %v", err)
	}
	if err := os.WriteFile(waitFile, []byte("go"), 0o600); err != nil {
		t.Fatalf("signal helper: %v", err)
	}

	childPID := waitForPIDFile(t, childPIDFile)
	grandchildPID := waitForPIDFile(t, grandchildPIDFile)
	assertNotRunningEventually(t, childPID)
	assertRunning(t, parent.Process.Pid)
	assertRunning(t, grandchildPID)

	if err := KillTree(strconv.Itoa(parent.Process.Pid), "devsy-test-worker"); err != nil {
		t.Fatalf("Kill(parent): %v", err)
	}

	assertNotRunningEventually(t, parent.Process.Pid)
	assertNotRunningEventually(t, grandchildPID)
}
