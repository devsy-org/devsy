//go:build !windows

package shell

import (
	"os/exec"
	"syscall"

	"mvdan.cc/sh/v3/interp"
)

func providerExecSignalStatus(err *exec.ExitError) (interp.ExitStatus, bool) {
	if status, ok := err.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		code := 128 + int(status.Signal())
		if code < 0 || code > 255 {
			return 0, false
		}
		return interp.ExitStatus(uint8(code)), true
	}
	return 0, false
}
