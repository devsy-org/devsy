//go:build windows

package dockerwsl

import (
	"os/exec"
	"syscall"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
)

const (
	createNewProcessGroup = 0x00000200
	createNoWindow        = 0x08000000
)

// windowsHost is the native Windows implementation of hostPlatform.
type windowsHost struct{}

func newHostPlatform(*runenv.Dependencies) hostPlatform { return windowsHost{} }

func (windowsHost) LookPath(file string) (string, error) { return exec.LookPath(file) }

// StartDetached starts a hidden wsl.exe that keeps the Linux daemon's session
// alive after this command exits. WSL ends background Linux processes once the
// last wsl.exe attached to them goes away, so the process is released, not
// waited on, and gets its own process group and no console window.
func (windowsHost) StartDetached(path string, args []string) error {
	cmd := exec.Command(path, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup | createNoWindow,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
