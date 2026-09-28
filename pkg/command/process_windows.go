//go:build windows

package command

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"golang.org/x/sys/windows"
)

const strongProcessIdentitySupported = true

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
		return false, fmt.Errorf(
			"wait for process %d returned unexpected result %#x",
			parsed,
			event,
		)
	}
}

func processRefForStartedProcess(pid int, workerName string) (ProcessRef, error) {
	jobID, err := jobNameFor(workerName)
	if err != nil {
		return ProcessRef{}, err
	}
	identity, err := processTreeIdentity(pid)
	if err != nil {
		return ProcessRef{}, err
	}
	return ProcessRef{
		PID:      pid,
		TreeKind: ProcessTreeWindowsJob,
		TreeID:   jobID,
		Identity: identity,
	}, nil
}

func terminateProcessRef(ref ProcessRef) error {
	if ref.TreeKind == ProcessTreeWindowsJob {
		terminated, err := terminateNamedJob(ref.TreeID)
		if err != nil {
			return err
		}
		if terminated {
			return nil
		}
		running, err := savedWorkerRunning(ref)
		if err != nil {
			return err
		}
		if !running {
			return nil
		}
		return fmt.Errorf(
			"worker job %s is unavailable while process %d is running",
			ref.TreeID,
			ref.PID,
		)
	}
	if ref.TreeKind == ProcessTreeLegacyPID {
		pid := strconv.Itoa(ref.PID)
		if ref.Identity != "" {
			return killTreeWithIdentity(pid, ref.TreeID, ref.Identity)
		}
		return terminateLegacyRefWithoutIdentity(ref)
	}
	return fmt.Errorf("unsupported Windows process tree kind %q", ref.TreeKind)
}

// A record written before process identities existed has nothing to compare a
// reused PID against, so require a Devsy executable before taskkill takes down
// a whole tree.
func terminateLegacyRefWithoutIdentity(ref ProcessRef) error {
	image, err := processImageName(ref.PID)
	if err != nil {
		return err
	}
	if image == "" {
		return nil
	}
	if !isDevsyImage(image) {
		return fmt.Errorf(
			"refusing to terminate process %d: %q is not a %s worker",
			ref.PID,
			filepath.Base(image),
			config.RepoName,
		)
	}
	return killTree(strconv.Itoa(ref.PID), ref.TreeID)
}

func isDevsyImage(image string) bool {
	name := strings.ToLower(filepath.Base(image))
	name = strings.TrimSuffix(name, filepath.Ext(name))
	return name == config.RepoName || strings.HasPrefix(name, config.RepoName+"-")
}

// processImageName returns an empty string when the process no longer exists.
func processImageName(pid int) (string, error) {
	handle, err := windows.OpenProcess(
		windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(pid),
	)
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return "", nil
		}
		return "", fmt.Errorf("open process %d for image name: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	const maxPath = 32768
	buffer := make([]uint16, maxPath)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return "", fmt.Errorf("read image name for process %d: %w", pid, err)
	}
	return windows.UTF16ToString(buffer[:size]), nil
}

func savedWorkerRunning(ref ProcessRef) (bool, error) {
	current, err := processTreeIdentity(ref.PID)
	if err != nil {
		return false, err
	}
	if current == "" || (ref.Identity != "" && current != ref.Identity) {
		return false, nil
	}
	return isRunning(strconv.Itoa(ref.PID))
}

func cleanupExitedProcessRef(ref ProcessRef) error {
	if ref.TreeKind == ProcessTreeWindowsJob {
		terminated, err := terminateNamedJob(ref.TreeID)
		if err != nil || terminated {
			return err
		}
		running, err := savedWorkerRunning(ref)
		if err != nil {
			return err
		}
		if running {
			return fmt.Errorf(
				"worker job %s is unavailable while process %d is running",
				ref.TreeID,
				ref.PID,
			)
		}
		return nil
	}
	if ref.TreeKind == ProcessTreeLegacyPID && ref.Identity != "" {
		return killTreeAfterWorkerExit(strconv.Itoa(ref.PID), ref.TreeID, ref.Identity)
	}
	return nil
}

func abortSupervisedLaunch(pid int, workerName string) error {
	terminated, err := terminateJobForPID(workerName, pid)
	if err != nil {
		return err
	}
	if terminated {
		return nil
	}
	return killTree(strconv.Itoa(pid), "")
}

func killTree(pid, treeName string) error {
	parsed, err := parsePID(pid)
	if err != nil {
		return err
	}

	var jobErr error
	if treeName != "" {
		if terminated, err := terminateJobForPID(treeName, parsed); err == nil && terminated {
			return nil
		} else if err != nil {
			jobErr = err
		}
	}

	running, err := isRunning(pid)
	if err != nil {
		return err
	}
	if !running {
		if jobErr != nil {
			return fmt.Errorf("terminate process tree %s: %w", treeName, jobErr)
		}
		return nil
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

	if jobErr != nil {
		return fmt.Errorf("terminate process tree %s: %w", treeName, jobErr)
	}
	return verifyTerminated(parsed)
}

func killTreeWithIdentity(pid, treeName, identity string) error {
	parsed, err := parsePID(pid)
	if err != nil {
		return err
	}
	if identity == "" {
		return fmt.Errorf("process identity is required to terminate worker %d", parsed)
	}
	if treeName == "" {
		return fmt.Errorf("worker job identity is required")
	}
	currentIdentity, err := processTreeIdentity(parsed)
	if err != nil {
		return fmt.Errorf("read process identity for worker %d: %w", parsed, err)
	}
	if currentIdentity == "" {
		return fmt.Errorf("process %d exited before its identity was verified", parsed)
	}
	if currentIdentity != identity {
		return fmt.Errorf("process %d identity does not match the saved worker", parsed)
	}
	terminated, err := terminateJobForPID(treeName, parsed)
	if err != nil {
		return err
	}
	if terminated {
		return nil
	}
	running, err := isRunning(pid)
	if err != nil {
		return err
	}
	if !running {
		return nil
	}
	return fmt.Errorf(
		"worker job %s is unavailable while process %d is still running",
		treeName,
		parsed,
	)
}

func killTreeAfterWorkerExit(pid, treeName, identity string) error {
	if _, err := parsePID(pid); err != nil {
		return err
	}
	if treeName == "" {
		return fmt.Errorf("process tree name is required after worker exit")
	}
	terminated, err := terminateJobAfterWorkerExit(treeName)
	if err != nil {
		return err
	}
	if !terminated {
		if identity == "" {
			return fmt.Errorf(
				"worker job %s is unavailable; cannot verify descendants without process identity",
				treeName,
			)
		}
		// SetPID runs only after launch assigns the worker to a kill-on-close job.
	}
	return nil
}

func processTreeIdentity(pid int) (string, error) {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return "", nil
		}
		return "", fmt.Errorf("open process %d for identity: %w", pid, err)
	}
	defer func() { _ = windows.CloseHandle(process) }()

	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(process, &created, &exited, &kernel, &user); err != nil {
		return "", fmt.Errorf("read process %d creation time: %w", pid, err)
	}
	return strconv.FormatInt(created.Nanoseconds(), 10), nil
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
