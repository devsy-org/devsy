//go:build linux || darwin || unix

package task

import (
	"os/exec"
	"syscall"
)

func startupTestChild() *exec.Cmd {
	child := exec.Command("sleep", "30")
	child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return child
}
