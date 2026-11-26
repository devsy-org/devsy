//go:build linux || darwin || unix

package command

import (
	"os/exec"
	"syscall"
)

func processExitTestChild() *exec.Cmd {
	child := exec.Command("sleep", "30")
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return child
}
