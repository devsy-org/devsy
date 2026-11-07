//go:build !windows

package pty_test

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/pty"
	"github.com/stretchr/testify/suite"
)

type PTYOtherSuite struct {
	suite.Suite
}

func TestPTYOtherSuite(t *testing.T) {
	suite.Run(t, new(PTYOtherSuite))
}

func (s *PTYOtherSuite) TestPidIsChildPid() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pc, ps, err := pty.Start(pty.CommandContext(ctx, "/bin/sh", "-c", "echo $$; exec sleep 30"))
	s.Require().NoError(err)
	defer func() { _ = pc.Close() }()

	line, err := bufio.NewReader(pc.OutputReader()).ReadString('\n')
	s.Require().NoError(err)
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	s.Require().NoError(err)
	s.Equal(pid, ps.Pid())
}

func (s *PTYOtherSuite) TestWithTTYOwner() {
	uid, gid := os.Getuid(), os.Getgid()
	if os.Geteuid() == 0 {
		uid, gid = 4321, 4322
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pc, _, err := pty.Start(
		pty.CommandContext(ctx, "/bin/sh", "-c", "tty; exec sleep 30"),
		pty.WithPTYOption(pty.WithTTYOwner(uid, gid)),
	)
	s.Require().NoError(err)
	defer func() { _ = pc.Close() }()

	if os.Geteuid() != 0 {
		return
	}

	line, err := bufio.NewReader(pc.OutputReader()).ReadString('\n')
	s.Require().NoError(err)
	info, err := os.Stat(strings.TrimSpace(line))
	s.Require().NoError(err)
	stat, ok := info.Sys().(*syscall.Stat_t)
	s.Require().True(ok)
	s.EqualValues(uid, stat.Uid)
	s.EqualValues(gid, stat.Gid)
}
