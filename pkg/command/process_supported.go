//go:build linux || darwin || unix

package command

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

func isRunning(pid string) (bool, error) {
	parsedPid, err := strconv.Atoi(pid)
	if err != nil {
		return false, err
	}
	if parsedPid <= 0 {
		return false, fmt.Errorf("invalid PID %d: must be positive", parsedPid)
	}

	process, err := os.FindProcess(parsedPid)
	if err != nil {
		return false, err
	}

	err = process.Signal(syscall.Signal(0))
	if err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return false, nil
		}
		if errors.Is(err, syscall.EPERM) {
			return true, nil
		}
		return false, fmt.Errorf("check process %d: %w", parsedPid, err)
	}

	return true, nil
}

// Detached workers use a session identity to distinguish their group after the leader exits.
func killTree(pid, treeName string) error {
	parsedPid, err := strconv.Atoi(pid)
	if err != nil {
		return err
	}
	if parsedPid <= 0 {
		return fmt.Errorf("invalid PID %d: must be positive", parsedPid)
	}
	identity, err := processTreeIdentity(parsedPid)
	if err != nil {
		return fmt.Errorf("read process tree identity for worker %d: %w", parsedPid, err)
	}
	if identity == "" {
		if treeName == "" {
			return nil
		}
		return checkUnidentifiedProcessGroup(parsedPid, treeName)
	}
	return killTreeWithIdentity(pid, treeName, identity)
}

func killTreeWithIdentity(pid, treeName, identity string) error {
	parsedPid, err := strconv.Atoi(pid)
	if err != nil {
		return err
	}
	if parsedPid <= 0 {
		return fmt.Errorf("invalid PID %d: must be positive", parsedPid)
	}
	if treeName == "" {
		return killSingleProcessWithIdentity(pid, parsedPid, identity)
	}
	if identity == "" {
		return errors.New("process tree identity is required to terminate a worker")
	}
	return killWorkerTreeWithIdentity(parsedPid, treeName, identity)
}

func killSingleProcessWithIdentity(pid string, parsedPid int, identity string) error {
	if identity == "" {
		running, err := isRunning(pid)
		if err != nil {
			return err
		}
		if !running {
			return nil
		}
		return errors.New("process identity is required to terminate a process")
	}
	return signalProcessTree(parsedPid, true, identity)
}

func killWorkerTreeWithIdentity(parsedPid int, treeName, identity string) error {
	groupMatches, err := processGroupMatchesIdentity(parsedPid, identity)
	if err != nil {
		return fmt.Errorf("verify process group %d for worker %s: %w", parsedPid, treeName, err)
	}
	if groupMatches {
		return signalProcessTree(-parsedPid, false, identity)
	}
	currentIdentity, err := processTreeIdentity(parsedPid)
	if err != nil {
		return fmt.Errorf("read process tree identity for worker %d: %w", parsedPid, err)
	}
	// The saved group is empty, so a different identity at this PID is a reused process.
	if currentIdentity != identity {
		return nil
	}
	return signalProcessTree(parsedPid, false, identity)
}

func killTreeAfterWorkerExit(pid, treeName, identity string) error {
	parsedPid, err := strconv.Atoi(pid)
	if err != nil {
		return err
	}
	if parsedPid <= 0 {
		return fmt.Errorf("invalid PID %d: must be positive", parsedPid)
	}
	if treeName == "" {
		return errors.New("process tree name is required after worker exit")
	}
	if identity == "" {
		return checkUnidentifiedProcessGroup(parsedPid, treeName)
	}
	matches, err := processGroupMatchesIdentity(parsedPid, identity)
	if err != nil {
		return fmt.Errorf(
			"verify process group %d for worker tree %s: %w",
			parsedPid,
			treeName,
			err,
		)
	}
	if !matches {
		return nil
	}
	return signalProcessTree(-parsedPid, false, identity)
}

func checkUnidentifiedProcessGroup(pid int, treeName string) error {
	if err := syscall.Kill(-pid, 0); errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return fmt.Errorf("verify process group %d for worker tree %s", pid, treeName)
}

func signalProcessTree(target int, graceful bool, identity string) error {
	if err := syscall.Kill(target, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil // already exited
		}
		return fmt.Errorf("send SIGTERM to process target %d: %w", target, err)
	}
	if graceful {
		time.Sleep(2 * time.Second)
	}
	matches, err := signalTargetMatchesIdentity(target, identity)
	if err != nil {
		return fmt.Errorf("verify process target %d before SIGKILL: %w", target, err)
	}
	if !matches {
		return nil
	}
	err = syscall.Kill(target, syscall.SIGKILL)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("send SIGKILL to process target %d: %w", target, err)
	}
	return nil
}

func signalTargetMatchesIdentity(target int, identity string) (bool, error) {
	if identity == "" {
		return false, errors.New("process identity is required before SIGKILL")
	}
	if target < 0 {
		return processGroupMatchesIdentity(-target, identity)
	}
	currentIdentity, err := processTreeIdentity(target)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return false, nil
		}
		if errors.Is(err, syscall.EIO) && errors.Is(syscall.Kill(target, 0), syscall.ESRCH) {
			return false, nil
		}
		return false, err
	}
	return currentIdentity == identity, nil
}

// ownProcessTree is a no-op on Unix: the process group created by
// prepareBackgroundTree already scopes the tree.
func ownProcessTree(pid int, name string) error {
	return nil
}

func resumeBackgroundTree(pid int) error {
	return nil
}

func prepareBackgroundTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}
