//go:build !windows

package opener

import (
	"os/exec"
	"syscall"
)

func setDetachedProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
