//go:build windows

package docker

import "os/exec"

// setProcessGroupAttrs is a no-op on Windows.
func setProcessGroupAttrs(_ *exec.Cmd) {}

// killProcessGroup is a no-op on Windows, since Windows does not support process groups
// like Unix systems. It kills the single process.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
