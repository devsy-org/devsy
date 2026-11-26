//go:build windows

package server

import (
	"os"
	"syscall"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/ssh"
)

func mergeCommandSysProcAttr(existing *syscall.SysProcAttr) *syscall.SysProcAttr {
	if existing != nil {
		return existing
	}
	return &syscall.SysProcAttr{}
}

func forwardSignal(sig ssh.Signal, proc *os.Process) {
	log.Debugf("signal forwarding not supported on windows, ignoring %s", sig)
}

// osSignalFrom returns SIGTERM as a safe default on platforms (e.g., Windows)
// where SSH signal mapping is not meaningful. The parameter is intentionally
// unused to maintain API compatibility with the Unix implementation.
func osSignalFrom(_ ssh.Signal) os.Signal {
	return syscall.SIGTERM
}
