//go:build windows

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestRunnerKillsDirectProcessAndLeavesDescendantForRecovery(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("DEVSY_PODMAN_PROCESS_HELPER", "parent")
	t.Setenv("DEVSY_PODMAN_CHILD_PID_FILE", pidFile)
	result := (&ExecRunner{}).Run(
		context.Background(),
		CommandSpec{
			Path:    os.Args[0],
			Args:    []string{"-test.run=^TestRunnerProcessFixture$"},
			Timeout: 500 * time.Millisecond,
		},
	)
	if !result.TimedOut || result.ProcessID == 0 {
		t.Fatalf("parent did not time out: %+v", result)
	}
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("parent did not publish child PID: %v", err)
	}
	childPID64, err := strconv.ParseUint(string(data), 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Stderr, "direct_process_stopped=true") {
		t.Fatalf("timed-out direct process stop was not confirmed: %q", result.Stderr)
	}
	childPID := uint32(childPID64)
	child, err := windows.OpenProcess(
		windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		childPID,
	)
	if err != nil {
		t.Fatal("direct-process timeout killed its descendant")
	}
	defer func() { _ = windows.CloseHandle(child) }()
	if err := windows.TerminateProcess(child, 0); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerProcessFixture(_ *testing.T) {
	switch os.Getenv("DEVSY_PODMAN_PROCESS_HELPER") {
	case "child":
		time.Sleep(30 * time.Second)
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestRunnerProcessFixture$")
		child.Env = append(os.Environ(), "DEVSY_PODMAN_PROCESS_HELPER=child")
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		_ = os.WriteFile(
			os.Getenv("DEVSY_PODMAN_CHILD_PID_FILE"),
			[]byte(strconv.Itoa(child.Process.Pid)),
			0o600,
		)
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
}
