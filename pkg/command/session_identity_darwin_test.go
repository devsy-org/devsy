//go:build darwin

package command

import (
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"
)

type StartedSessionIdentitySuite struct{ suite.Suite }

const darwinProcessZombie = 5

func TestStartedSessionIdentitySuite(t *testing.T) {
	suite.Run(t, new(StartedSessionIdentitySuite))
}

func (s *StartedSessionIdentitySuite) TestUnreapedExitRetainsIdentity() {
	cmd := exec.Command("true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	s.Require().NoError(cmd.Start())
	s.T().Cleanup(func() { s.NoError(cmd.Wait()) })
	s.Require().Eventually(func() bool {
		p, err := unix.SysctlKinfoProc("kern.proc.pid", cmd.Process.Pid)
		return err == nil && p.Proc.P_stat == darwinProcessZombie
	}, 5*time.Second, time.Millisecond, "child must exit without being reaped")

	identity, err := StartedSessionIdentity(cmd.Process.Pid)
	s.Require().NoError(err)
	s.Contains(identity, strconv.Itoa(cmd.Process.Pid)+":")
}

func (s *StartedSessionIdentitySuite) TestOrdinaryGroupIsNotASession() {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	s.Require().NoError(cmd.Start())
	s.T().Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	_, err := StartedSessionIdentity(cmd.Process.Pid)
	s.Error(err, "a group leader in the parent's session must not be treated as its own session")
}

func (s *StartedSessionIdentitySuite) TestExitTransitionDoesNotNeedMutableGroupFields() {
	process := &unix.KinfoProc{}
	process.Proc.P_starttime.Sec = 100
	process.Proc.P_starttime.Usec = 200
	identity, err := darwinStartedSessionIdentity(42, -1, syscall.ESRCH, process)
	s.Require().NoError(err)
	s.Equal("42:100:200", identity)
}
