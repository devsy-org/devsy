package shell

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/suite"
)

const helperProcessEnv = "DEVSY_SHELL_TEST_HELPER_PROCESS"

func TestMain(m *testing.M) {
	if os.Getenv(helperProcessEnv) == "1" {
		_, _ = os.Stdout.WriteString("helper ran with " + os.Args[len(os.Args)-1])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type ExecutablePathTestSuite struct {
	suite.Suite
	executable string
}

func TestExecutablePathTestSuite(t *testing.T) {
	suite.Run(t, new(ExecutablePathTestSuite))
}

// SetupTest installs a copy of the test binary under a directory containing a
// space, like the binaries directory of a Windows profile such as "Jane Doe".
func (s *ExecutablePathTestSuite) SetupTest() {
	dir := filepath.Join(s.T().TempDir(), "Jane Doe", "binaries")
	s.Require().NoError(os.MkdirAll(dir, 0o750))

	name := "provider"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	s.executable = filepath.Join(dir, name)

	s.copyTestBinary(s.executable)
}

func (s *ExecutablePathTestSuite) TestCommandVariableWithSpacesInPathIsNotSplit() {
	output, err := s.run(
		"${SSH_PROVIDER} init",
		"SSH_PROVIDER="+s.executable,
		helperProcessEnv+"=1",
	)

	s.Require().NoError(err)
	s.Equal("helper ran with init", output)
}

func (s *ExecutablePathTestSuite) TestShortVariableFormWithSpacesInPathIsNotSplit() {
	output, err := s.run(
		"$SSH_PROVIDER init",
		"SSH_PROVIDER="+s.executable,
		helperProcessEnv+"=1",
	)

	s.Require().NoError(err)
	s.Equal("helper ran with init", output)
}

func (s *ExecutablePathTestSuite) TestCommandVariableWithArgumentsStillSplits() {
	output, err := s.run("${CMD} world", "CMD=echo hello")

	s.Require().NoError(err)
	s.Equal("hello world\n", output)
}

func (s *ExecutablePathTestSuite) TestReassignedCommandVariableStillSplits() {
	output, err := s.run("CMD='echo hello'; $CMD world", "CMD="+s.executable)

	s.Require().NoError(err)
	s.Equal("hello world\n", output)
}

func (s *ExecutablePathTestSuite) TestCommandVariableAssignedInScriptIsNotSplit() {
	output, err := s.run(
		"PROVIDER='"+s.executable+"'; ${PROVIDER} init",
		helperProcessEnv+"=1",
	)

	s.Require().NoError(err)
	s.Equal("helper ran with init", output)
}

func (s *ExecutablePathTestSuite) TestLiteralCommandIsNotJoinedWithUnrelatedVariable() {
	dir := s.T().TempDir()
	ext := filepath.Ext(s.executable)
	tool := filepath.Join(dir, "tool"+ext)
	spacedTool := filepath.Join(dir, "tool status"+ext)
	s.copyTestBinary(tool)
	s.copyTestBinary(spacedTool)

	output, err := s.run(
		"'"+tool+"' status; $CMD",
		"CMD="+spacedTool,
		helperProcessEnv+"=1",
	)

	s.Require().NoError(err)
	s.Equal("helper ran with status"+"helper ran with "+spacedTool, output)
}

func (s *ExecutablePathTestSuite) TestVariableArgumentWithSpacesStillSplits() {
	output, err := s.run("echo ${ARGS}", "ARGS=a b")

	s.Require().NoError(err)
	s.Equal("a b\n", output)
}

func (s *ExecutablePathTestSuite) run(command string, environ ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	err := RunEmulatedShell(context.Background(), &CommandRunner{
		Command: command,
		Stdout:  &stdout,
		Stderr:  &stderr,
		Environ: append(os.Environ(), environ...),
	})
	return stdout.String(), err
}

func (s *ExecutablePathTestSuite) copyTestBinary(target string) {
	self, err := os.Executable()
	s.Require().NoError(err)
	source, err := os.Open(self) //nolint:gosec // copies this test binary
	s.Require().NoError(err)
	defer func() { _ = source.Close() }()
	file, err := os.OpenFile( //nolint:gosec // the copy must be executable
		target, os.O_CREATE|os.O_WRONLY, 0o700,
	)
	s.Require().NoError(err)
	_, err = io.Copy(file, source)
	s.Require().NoError(err)
	s.Require().NoError(file.Close())
}
