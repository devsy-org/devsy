//go:build linux || darwin || unix

package command

import (
	"errors"
	"os"
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

func kill(pid string) error {
	parsedPid, err := strconv.Atoi(pid)
	if err != nil {
		return err
	}

	if err := syscall.Kill(parsedPid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil // already exited
		}
		return err
	}
	time.Sleep(2 * time.Second)
	if err := syscall.Kill(parsedPid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
