//go:build windows

package clientimplementation

import (
	"io/fs"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// Combining DETACHED_PROCESS with CREATE_NEW_CONSOLE makes the real CreateProcess
// call fail with ERROR_INVALID_PARAMETER, the error reported in issue 1638.
func (s *ProviderCommandErrorTestSuite) TestRealInvalidParameterStartFailureGetsHint() {
	self, err := os.Executable()
	s.Require().NoError(err)
	cmd := exec.Command(self)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_CONSOLE,
	}
	startErr := cmd.Start()
	s.Require().ErrorIs(startErr, windows.ERROR_INVALID_PARAMETER)

	err = providerCommandError(self, nil, startErr)

	s.ErrorIs(err, windows.ERROR_INVALID_PARAMETER)
	s.Contains(err.Error(), invalidProcessParameterHint)
	s.Contains(err.Error(), "The parameter is incorrect")
}

func (s *ProviderCommandErrorTestSuite) TestInvalidParameterFromOutputWriterGetsNoHint() {
	writeErr := &fs.PathError{Op: "write", Path: "stdout", Err: windows.ERROR_INVALID_PARAMETER}

	err := providerCommandError("provider", nil, writeErr)

	s.ErrorIs(err, windows.ERROR_INVALID_PARAMETER)
	s.NotContains(err.Error(), invalidProcessParameterHint)
}
