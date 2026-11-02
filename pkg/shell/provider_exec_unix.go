//go:build !windows

package shell

import (
	"os/exec"
	"syscall"

	"mvdan.cc/sh/v3/interp"
)

func providerExecSignalStatus(err *exec.ExitError) (interp.ExitStatus, bool) {
	if status, ok := err.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return interp.ExitStatus(128 + status.Signal()), true
	}
	return 0, false
}
