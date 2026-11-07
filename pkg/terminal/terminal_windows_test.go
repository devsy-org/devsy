//go:build windows

package terminal

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/windows"
)

type WindowsTerminalSuite struct {
	suite.Suite
}

func TestWindowsTerminalSuite(t *testing.T) {
	suite.Run(t, new(WindowsTerminalSuite))
}

func (s *WindowsTerminalSuite) TestVTArrowKeysArrive() {
	r, w, err := os.Pipe()
	s.Require().NoError(err)
	defer func() { s.NoError(r.Close()) }()
	defer func() { s.NoError(w.Close()) }()
	input, err := NewInput(r)
	s.Require().NoError(err)
	defer func() { s.NoError(input.Close()) }()
	_, err = w.Write([]byte("\x1b[A\x1b[B\x1b[C\x1b[D"))
	s.Require().NoError(err)
	buf := make([]byte, 12)
	done := make(chan error, 1)
	go func() {
		_, readErr := input.Read(buf)
		done <- readErr
	}()
	s.Require().Eventually(func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	s.Equal([]byte("\x1b[A\x1b[B\x1b[C\x1b[D"), buf)
}

func (s *WindowsTerminalSuite) TestConsoleInputCancelUnblocksRead() {
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode); err != nil {
		s.T().Skip("stdin is not a console")
	}
	input, err := NewInput(os.Stdin)
	s.Require().NoError(err)
	defer func() { s.NoError(input.Close()) }()
	done := make(chan error, 1)
	go func() { _, readErr := input.Read(make([]byte, 1)); done <- readErr }()
	canceled := make(chan bool, 1)
	go func() { canceled <- input.Cancel() }()
	select {
	case ok := <-canceled:
		s.True(ok)
	case <-time.After(5 * time.Second):
		s.T().Fatal("cancel blocked")
	}
	select {
	case readErr := <-done:
		s.Error(readErr)
	case <-time.After(5 * time.Second):
		s.T().Fatal("read did not unblock")
	}
}
