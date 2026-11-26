package clientimplementation

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestProviderShellProcessConsoleIsolation(t *testing.T) {
	cmd := &exec.Cmd{
		SysProcAttr: &syscall.SysProcAttr{CreationFlags: windows.CREATE_UNICODE_ENVIRONMENT},
	}
	configureProviderShellProcess(cmd)
	require.NotZero(t, cmd.SysProcAttr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP)
	require.NotZero(t, cmd.SysProcAttr.CreationFlags&windows.CREATE_UNICODE_ENVIRONMENT)
}
