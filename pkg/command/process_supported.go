//go:build linux || darwin || unix

package command

import (
	"errors"
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

	process, err := os.FindProcess(parsedPid)
	if err != nil {
		return false, err
	}

	err = process.Signal(syscall.Signal(0))
	if err != nil {
		return false, nil
	}

	return true, nil
}

// A background process that leads its own group (prepareBackgroundTree) is
// signaled group-wide so descendants that outlive it still die with it.
func killTree(pid, _ string) error {
	parsedPid, err := strconv.Atoi(pid)
	if err != nil {
		return err
	}

	target := parsedPid
	if pgid, err := syscall.Getpgid(parsedPid); err == nil && pgid == parsedPid {
		target = -parsedPid
	}

	if err := syscall.Kill(target, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil // already exited
		}
		return err
	}
	time.Sleep(2 * time.Second)
	err = syscall.Kill(target, syscall.SIGKILL)
	if err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

// ownProcessTree is a no-op on Unix: the process group created by
// prepareBackgroundTree already scopes the tree.
func ownProcessTree(pid int, name string) error {
	return nil
}

func prepareBackgroundTree(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}
