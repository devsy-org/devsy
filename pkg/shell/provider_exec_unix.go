//go:build !windows

package shell

import (
	"os"
	"os/exec"
	"syscall"

	"mvdan.cc/sh/v3/interp"
)

func providerPrivateDirectory(_ string, info os.FileInfo) bool {
	return info.Mode().Perm() == 0o700
}

func providerPrivateScript(info os.FileInfo) bool {
	return info.Mode().Perm() == 0o600
}

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
