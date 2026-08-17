//go:build !windows

package docker

import (
	"os/exec"
	"syscall"
)

// setProcessGroupAttrs puts cmd in its own process group so
// killProcessGroup can terminate the whole tree it spawns.
func setProcessGroupAttrs(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup sends SIGKILL to cmd's entire process group.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid {
		if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
			if killErr := syscall.Kill(-pgid, syscall.SIGKILL); killErr == nil {
				return nil
			}
		}
	}
	return cmd.Process.Kill()
}
