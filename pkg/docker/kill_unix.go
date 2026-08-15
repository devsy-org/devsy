//go:build !windows

package docker

import (
	"os/exec"
	"syscall"
)

// cmdSysProcAttr puts the command in its own process group so killCmd can
// terminate the whole group, not just the immediate child.
func cmdSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killCmd terminates cmd's process group. A privilege-elevation helper (sudo,
// pkexec, doas) forwards signals it can catch to the command it launches, but
// SIGKILL cannot be caught: killing only the helper's PID leaves its child
// (e.g. the actual podman process) running and orphaned, holding the pipes
// runCmd is waiting on open forever. Signaling the negative PID targets every
// process cmdSysProcAttr grouped together instead of just the leader.
func killCmd(cmd *exec.Cmd) error {
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		if killErr := syscall.Kill(-pgid, syscall.SIGKILL); killErr == nil {
			return nil
		}
	}
	return cmd.Process.Kill()
}
