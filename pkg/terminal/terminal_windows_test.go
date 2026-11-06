//go:build windows

package terminal

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type WindowsTerminalSuite struct {
	suite.Suite
}

func TestWindowsTerminalSuite(t *testing.T) {
	suite.Run(t, new(WindowsTerminalSuite))
}

func (s *WindowsTerminalSuite) TestInputCancelUnblocksRead() {
	r, w, err := os.Pipe()
	s.Require().NoError(err)
	defer r.Close()
	defer w.Close()
	input, err := NewInput(r)
	s.Require().NoError(err)
	done := make(chan error, 1)
	go func() {
		_, readErr := input.Read(make([]byte, 1))
		done <- readErr
	}()
	s.True(input.Cancel())
	select {
	case err = <-done:
		s.Error(err)
	case <-time.After(5 * time.Second):
		s.Fail("blocked read was not canceled")
	}
	s.NoError(input.Close())
}

func (s *WindowsTerminalSuite) TestVTArrowKeysArrive() {
	r, w, err := os.Pipe()
	s.Require().NoError(err)
	defer r.Close()
	defer w.Close()
	input, err := NewInput(r)
	s.Require().NoError(err)
	defer input.Close()
	_, err = w.Write([]byte("\x1b[A\x1b[B\x1b[C\x1b[D"))
	s.Require().NoError(err)
	buf := make([]byte, 12)
	done := make(chan error, 1)
	go func() {
		_, readErr := input.Read(buf)
		done <- readErr
	}()
	s.Eventually(func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	s.Equal([]byte("\x1b[A\x1b[B\x1b[C\x1b[D"), buf)
}
