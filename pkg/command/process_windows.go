//go:build windows

package command

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code GetExitCodeProcess reports for a live process.
const stillActive uint32 = 259

func isRunning(pid string) (bool, error) {
	parsed, err := parsePID(pid)
	if err != nil {
		return false, err
	}

	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(parsed),
	)
	if err != nil {
		// OpenProcess reports a nonexistent PID as ERROR_INVALID_PARAMETER;
		// anything else (e.g. access denied) is a real query failure.
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return false, nil
		}
		return false, fmt.Errorf("open process %d: %w", parsed, err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false, fmt.Errorf("query process %d: %w", parsed, err)
	}
	return exitCode == stillActive, nil
}

func kill(pid string) error {
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

	// /T takes down the worker's descendants as well: a detached up worker
	// can spawn children (e.g. devsy workspace ssh --stdio) that keep owning
	// transport resources. /F stands in for SIGKILL; Windows has no SIGTERM
	// equivalent for arbitrary processes.
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

	stillRunning, err := isRunning(pid)
	if err != nil {
		return err
	}
	if stillRunning {
		return fmt.Errorf("taskkill /PID %d /T /F reported success but the process is still running", parsed)
	}
	return nil
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

// truncateOutput bounds taskkill's captured output; the text itself is
// localized and never parsed.
func truncateOutput(output string) string {
	const maxOutput = 256
	if len(output) > maxOutput {
		return output[:maxOutput] + "..."
	}
	return output
}
