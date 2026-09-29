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
		if errors.Is(err, syscall.EPERM) {
			return true, nil
		}
		if isProcessGone(err) || errors.Is(err, os.ErrProcessDone) {
			return false, nil
		}
		return false, fmt.Errorf("check process %d: %w", parsedPid, err)
	}

	return true, nil
}

func processRefForStartedProcess(pid int, _ string) (ProcessRef, error) {
	identity, err := processTreeIdentity(pid)
	if err != nil {
		if !strongProcessIdentitySupported {
			identity = ""
		} else {
			return ProcessRef{}, err
		}
	}
	return ProcessRef{
		PID:      pid,
		TreeKind: ProcessTreeUnixGroup,
		TreeID:   strconv.Itoa(pid),
		Identity: identity,
	}, nil
}

func terminateProcessRef(ref ProcessRef) error {
	if ref.PID <= 0 {
		return fmt.Errorf("invalid worker PID %d", ref.PID)
	}
	if ref.TreeKind == ProcessTreeLegacyPID {
		if ref.Identity != "" {
			return killTreeWithIdentity(strconv.Itoa(ref.PID), ref.TreeID, ref.Identity)
		}
		return terminateUnidentifiedProcessGroup(ref.PID, strconv.Itoa(ref.PID))
	}
	if ref.TreeKind != ProcessTreeUnixGroup {
		return fmt.Errorf("unsupported Unix process tree kind %q", ref.TreeKind)
	}
	groupID := ref.TreeID
	if groupID == "" {
		groupID = strconv.Itoa(ref.PID)
	}
	if ref.Identity != "" {
		return killTreeWithIdentity(groupID, "detached-task", ref.Identity)
	}
	return terminateUnidentifiedProcessGroup(ref.PID, groupID)
}

func terminateUnidentifiedProcessGroup(pid int, groupID string) error {
	if !strongProcessIdentitySupported {
		return fmt.Errorf("cannot terminate worker group %d without process identity", pid)
	}
	identity, err := processTreeIdentity(pid)
	if err != nil {
		return fmt.Errorf("read process identity for worker %d: %w", pid, err)
	}
	if identity == "" {
		return terminateUnidentifiedExitedGroup(pid)
	}
	if err := killTreeWithIdentity(groupID, "detached-task", identity); err != nil {
		return err
	}
	return waitForUnidentifiedGroupExit(pid, identity)
}

func waitForUnidentifiedGroupExit(pid int, identity string) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		matches, err := processGroupMatchesIdentity(pid, identity)
		if err != nil {
			return fmt.Errorf("verify worker group %d after termination: %w", pid, err)
		}
		if !matches {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("worker group %d still has processes after termination", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A worker whose leader exited may still leave members behind.
func processGroupExists(pid int) bool {
	err := syscall.Kill(-pid, 0)
	if isProcessGone(err) {
		return false
	}
	// EPERM means the group exists but belongs to another user.
	return true
}

func cleanupExitedProcessRef(ref ProcessRef) error {
	if ref.TreeKind == ProcessTreeLegacyPID {
		if ref.Identity == "" {
			return terminateUnidentifiedExitedGroup(ref.PID)
		}
		return killTreeAfterWorkerExit(strconv.Itoa(ref.PID), ref.TreeID, ref.Identity)
	}
	if ref.TreeKind != ProcessTreeUnixGroup {
		return nil
	}
	groupID := ref.TreeID
	if groupID == "" {
		groupID = strconv.Itoa(ref.PID)
	}
	if ref.Identity == "" {
		return terminateUnidentifiedExitedGroup(ref.PID)
	}
	return killTreeAfterWorkerExit(groupID, "detached-task", ref.Identity)
}

// Never signals the group: after the leader exits the PGID may be reused.
func terminateUnidentifiedExitedGroup(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid worker PID %d", pid)
	}
	if processGroupExists(pid) {
		return fmt.Errorf(
			"worker group %d still has processes and no saved identity identifies them",
			pid,
		)
	}
	return nil
}

func abortSupervisedLaunch(pid int, _ string) error {
	for _, target := range []int{-pid, pid} {
		if err := syscall.Kill(target, syscall.SIGKILL); err != nil &&
			!isProcessGone(err) {
			return fmt.Errorf("terminate uncommitted worker %d: %w", pid, err)
		}
	}
	return nil
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
		return killUnidentifiedTree(parsedPid, treeName)
	}

	return killTreeWithIdentity(pid, treeName, identity)
}

func killUnidentifiedTree(parsedPid int, treeName string) error {
	if !strongProcessIdentitySupported && treeName == "" {
		if err := syscall.Kill(parsedPid, syscall.SIGTERM); err != nil && !isProcessGone(err) {
			return fmt.Errorf("send SIGTERM to process %d: %w", parsedPid, err)
		}
		return nil
	}
	if treeName == "" {
		return nil
	}
	return checkUnidentifiedProcessGroup(parsedPid, treeName)
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
	err := syscall.Kill(-pid, 0)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verify process group %d for worker tree %s: %w", pid, treeName, err)
	}
	return fmt.Errorf(
		"verify process group %d for worker tree %s: saved process identity is unavailable",
		pid,
		treeName,
	)
}

func isProcessGone(err error) bool {
	return err != nil && errors.Is(err, syscall.ESRCH)
}

func signalProcessTree(target int, graceful bool, identity string) error {
	if err := syscall.Kill(target, syscall.SIGTERM); err != nil {
		if isProcessGone(err) {
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
	if err != nil && !isProcessGone(err) {
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
