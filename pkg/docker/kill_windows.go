//go:build windows

package docker

import (
	"os/exec"
	"syscall"
)

// cmdSysProcAttr is a no-op on Windows; process-group signaling for killCmd
// is POSIX-only.
func cmdSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

// killCmd terminates cmd's process directly. Windows has no equivalent of a
// POSIX process group signal here, so a privilege-elevation helper's child
// process is not guaranteed to die with it.
func killCmd(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}
