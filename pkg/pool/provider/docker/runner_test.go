package docker

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

const helperEnv = "DEVSY_POOL_FAKE_DOCKER"

// TestMain lets the test binary stand in for the docker binary: SetupSuite
// copies this executable to a directory on PATH named docker, and the first
// argument picks a behaviour.
func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) != "" {
		os.Exit(fakeDocker(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func write(f *os.File, s string) {
	if _, err := f.WriteString(s); err != nil {
		os.Exit(2)
	}
}

func fakeDocker(args []string) int {
	switch args[0] {
	case "echo":
		write(os.Stdout, strings.Join(args[1:], "\n")+"\n")
	case "fail":
		write(os.Stderr, strings.Repeat("e", 10<<10))
		return 3
	case "big":
		write(os.Stdout, strings.Repeat("x", 2000))
	case "sleep":
		time.Sleep(time.Minute)
	}
	return 0
}

type CLISuite struct {
	suite.Suite
	dir string
}

func TestCLISuite(t *testing.T) { suite.Run(t, new(CLISuite)) }

func (s *CLISuite) SetupSuite() {
	exe, err := os.Executable()
	s.Require().NoError(err)
	s.dir = s.T().TempDir()
	name := "docker"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dst := filepath.Join(s.dir, name)
	if err := os.Link(exe, dst); err != nil {
		s.Require().NoError(os.Symlink(exe, dst))
	}
}

func (s *CLISuite) SetupTest() {
	s.T().Setenv(helperEnv, "1")
	s.T().Setenv("PATH", s.dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func (s *CLISuite) TestArgumentsArriveVerbatimWithoutAShell() {
	args := []string{spaced, shellBait, "x; rm -rf /", flagAll, "`id`", "*"}
	out, err := s.cli().Run(s.T().Context(), append([]string{"echo"}, args...)...)
	s.Require().NoError(err)
	s.Equal(strings.Join(args, "\n")+"\n", strings.ReplaceAll(string(out), "\r\n", "\n"))
}

func (s *CLISuite) TestAFailedCommandKeepsAShortStderrAndOnlyTheVerb() {
	_, err := s.cli().Run(s.T().Context(), "fail", "operand-that-must-not-leak")
	var ce *CommandError
	s.Require().ErrorAs(err, &ce)
	s.Equal("fail operand-that-must-not-leak", ce.Command, "only two arguments are kept")
	s.Len(ce.Stderr, maxStderr)
	s.NotContains(err.Error(), "3rd")
}

func (s *CLISuite) TestOutputOverTheLimitIsAnError() {
	_, err := s.cli().Run(s.T().Context(), "big")
	s.ErrorIs(err, ErrOutputTooLarge)
}

func (s *CLISuite) TestACanceledContextStartsNothing() {
	ctx, cancel := context.WithCancel(s.T().Context())
	cancel()
	_, err := CLI{}.Run(ctx, "ls")
	s.ErrorIs(err, context.Canceled)
}

func (s *CLISuite) TestCancelKillsARunningCommandPromptly() {
	ctx, cancel := context.WithTimeout(s.T().Context(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := s.cli().Run(ctx, "sleep")
	s.ErrorIs(err, context.DeadlineExceeded)
	s.Less(time.Since(start), 10*time.Second)
}

func (s *CLISuite) cli() CLI { return CLI{MaxOutput: 1000} }
