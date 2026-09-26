//go:build windows

package command

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"time"

	"golang.org/x/sys/windows"
)

func isRunning(pid string) (bool, error) {
	parsed, err := parsePID(pid)
	if err != nil {
		return false, err
	}

	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(parsed))
	if err != nil {
		// OpenProcess reports a nonexistent PID as ERROR_INVALID_PARAMETER;
		// anything else (e.g. access denied) is a real query failure.
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false, nil
		}
		return false, fmt.Errorf("open process %d: %w", parsed, err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	event, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return false, fmt.Errorf("wait for process %d: %w", parsed, err)
	}
	switch event {
	case windows.WAIT_OBJECT_0:
		return false, nil
	case uint32(windows.WAIT_TIMEOUT):
		return true, nil
	default:
		return false, fmt.Errorf("wait for process %d returned unexpected result %#x", parsed, event)
	}
}

func killTree(pid, treeName string) error {
	parsed, err := parsePID(pid)
	if err != nil {
		return err
	}

	running, err := isRunning(pid)
	if err != nil {
		return err
	}
	if !running {
		return nil
	}

	// Job Object termination reaches orphaned descendants; workers launched
	// before job ownership existed fall through to taskkill.
	if treeName != "" {
		if terminated, jobErr := terminateJob(treeName); jobErr == nil && terminated {
			return verifyTerminated(parsed)
		}
	}

	// /T takes down the worker's descendants; /F stands in for SIGKILL,
	// which Windows lacks for arbitrary processes.
	cmd := exec.Command("taskkill", "/PID", strconv.Itoa(parsed), "/T", "/F")
	output, runErr := cmd.CombinedOutput()
	if runErr != nil {
		// The process may have exited on its own between the checks.
		stillRunning, checkErr := isRunning(pid)
		if checkErr == nil && !stillRunning {
			return nil
		}
		return fmt.Errorf(
			"taskkill /PID %d /T /F: %w: %s",
			parsed,
			runErr,
			truncateOutput(string(output)),
		)
	}

	return verifyTerminated(parsed)
}

// verifyTerminated allows a brief window for the exit to become visible
// after the termination call reported success.
func verifyTerminated(parsed int) error {
	pid := strconv.Itoa(parsed)
	deadline := time.Now().Add(5 * time.Second)
	for {
		stillRunning, err := isRunning(pid)
		if err != nil {
			return err
		}
		if !stillRunning {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process %d is still running after termination", parsed)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func parsePID(pid string) (int, error) {
	parsed, err := strconv.Atoi(pid)
	if err != nil {
		return 0, fmt.Errorf("invalid PID %q: %w", pid, err)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("invalid PID %d: must be positive", parsed)
	}
	return parsed, nil
}

// truncateOutput bounds captured taskkill output, which is localized and
// never parsed.
func truncateOutput(output string) string {
	const maxOutput = 256
	if len(output) > maxOutput {
		return output[:maxOutput] + "..."
	}
	return output
}
