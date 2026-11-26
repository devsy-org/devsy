//go:build windows

package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestTerminateStrayWindowsProcessesKillsMatchingByName verifies that a
// process matching one of the "interesting" Podman/WSL helper names (here,
// win-sshproxy.exe) is observed by matchingWindowsProcesses and forcefully
// terminated by terminateWindowsProcesses. It targets only its own spawned
// helper (not the system-wide terminateStrayWindowsProcesses sweep) so it
// cannot kill unrelated podman/wsl processes a developer may have running.
func TestTerminateStrayWindowsProcessesKillsMatchingByName(t *testing.T) {
	exePath := copySelfAs(t, "win-sshproxy.exe")

	cmd := exec.Command(exePath, "-test.run=^TestStrayProcessHelperFixture$")
	cmd.Env = append(os.Environ(), "DEVSY_PODMAN_STRAY_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	helper, ok := waitForInterestingProcess(uint32(cmd.Process.Pid), 5*time.Second)
	if !ok {
		t.Fatal("helper process was not observed as an interesting Windows process")
	}

	terminateWindowsProcesses([]processInfo{helper}, &Logger{Out: io.Discard, Err: io.Discard})

	if !waitForProcessExit(cmd, 5*time.Second) {
		t.Fatal("stray process survived the termination sweep")
	}
}

func waitForInterestingProcess(pid uint32, timeout time.Duration) (processInfo, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if process, ok := findInterestingProcess(pid); ok {
			return process, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return processInfo{}, false
}

func findInterestingProcess(pid uint32) (processInfo, bool) {
	for _, process := range matchingWindowsProcesses() {
		if process.PID == pid {
			return process, true
		}
	}
	return processInfo{}, false
}

func waitForProcessExit(cmd *exec.Cmd, timeout time.Duration) bool {
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case <-waitErr:
		return true
	case <-time.After(timeout):
		return false
	}
}

// TestStrayProcessHelperFixture is re-executed (copied to an interesting
// binary name) by TestTerminateStrayWindowsProcessesKillsMatchingByName; it
// otherwise no-ops under `go test`.
func TestStrayProcessHelperFixture(_ *testing.T) {
	if os.Getenv("DEVSY_PODMAN_STRAY_HELPER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

func copySelfAs(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(dest, data, 0o700); err != nil {
		t.Fatal(err)
	}
	return dest
}
