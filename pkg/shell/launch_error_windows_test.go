//go:build windows

package shell

import (
	"io/fs"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestWithLaunchHintAddsHintToRealInvalidParameterStartFailure(t *testing.T) {
	self, err := os.Executable()
	require.NoError(t, err)
	cmd := exec.Command(self)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_CONSOLE,
	}
	startErr := cmd.Start()
	require.ErrorIs(t, startErr, windows.ERROR_INVALID_PARAMETER)

	err = WithLaunchHint(startErr)

	require.ErrorIs(t, err, windows.ERROR_INVALID_PARAMETER)
	require.Contains(t, err.Error(), InvalidProcessParameterHint)
}

func TestWithLaunchHintIgnoresInvalidParameterFromWrites(t *testing.T) {
	writeErr := &fs.PathError{Op: "write", Path: "stdout", Err: windows.ERROR_INVALID_PARAMETER}

	require.Same(t, writeErr, WithLaunchHint(writeErr))
}
