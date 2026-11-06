//go:build !windows

package terminal

import (
	"os"
	"testing"

	"github.com/devsy-org/devsy/pkg/pty/ptytest"
	"github.com/stretchr/testify/suite"
)

type UnixTerminalSuite struct {
	suite.Suite
}

func TestUnixTerminalSuite(t *testing.T) {
	suite.Run(t, new(UnixTerminalSuite))
}

func (s *UnixTerminalSuite) TestAttachedForegroundAndBackgroundPTY() {
	s.True(isForegroundProcessGroup(22, 22))
	s.False(isForegroundProcessGroup(22, 23))
	device := ptytest.New(s.T())
	f, err := os.OpenFile(device.Name(), os.O_RDWR, 0)
	s.Require().NoError(err)
	defer f.Close()
	s.Error(Attached(f, f))
}

func (s *UnixTerminalSuite) TestRawModeRestoreAndRegistryOnPTY() {
	device := ptytest.New(s.T())
	f, err := os.OpenFile(device.Name(), os.O_RDWR, 0)
	s.Require().NoError(err)
	defer f.Close()
	restore, err := EnterRaw(f, f)
	s.Require().NoError(err)
	second, err := EnterRaw(f, f)
	s.Require().NoError(err)
	RestoreAll()
	restore()
	second()
	RestoreAll()
}
