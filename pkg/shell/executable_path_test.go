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
	executable      string
	executablePaths map[string]string
}

func TestExecutablePathTestSuite(t *testing.T) {
	suite.Run(t, new(ExecutablePathTestSuite))
}

func (s *ExecutablePathTestSuite) SetupTest() {
	dir := filepath.Join(s.T().TempDir(), "Jane Doe", "binaries")
	s.Require().NoError(os.MkdirAll(dir, 0o750))

	name := "provider"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	s.executable = filepath.Join(dir, name)

	s.copyTestBinary(s.executable)
	s.executablePaths = map[string]string{
		"SSH_PROVIDER": s.executable,
		"CMD":          s.executable,
		"PROVIDER":     s.executable,
	}
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

func (s *ExecutablePathTestSuite) TestLiteralCommandIsNotJoinedWithUnrelatedVariable() {
	dir := s.T().TempDir()
	ext := filepath.Ext(s.executable)
	tool := filepath.Join(dir, "tool"+ext)
	spacedTool := tool + " status" + ext
	s.copyTestBinary(tool)
	s.copyTestBinary(spacedTool)

	s.executablePaths["CMD"] = spacedTool
	output, err := s.run(
		"'"+tool+"' status"+ext+"; $CMD",
		"CMD="+spacedTool,
		helperProcessEnv+"=1",
	)

	s.Require().NoError(err)
	s.Equal("helper ran with status"+ext+"helper ran with "+spacedTool, output)
}

func (s *ExecutablePathTestSuite) TestRelativePathIsResolvedAgainstShellDirectory() {
	shellDir := s.T().TempDir()
	relative := filepath.Join("spaced dir", filepath.Base(s.executable))
	s.Require().NoError(os.MkdirAll(filepath.Join(shellDir, "spaced dir"), 0o750))
	s.copyTestBinary(filepath.Join(shellDir, relative))
	s.executablePaths["PROVIDER"] = relative

	output, err := s.run(
		"cd '"+shellDir+"'; ${PROVIDER} init",
		"PROVIDER="+relative,
		helperProcessEnv+"=1",
	)

	s.Require().NoError(err)
	s.Equal("helper ran with init", output)
}

func (s *ExecutablePathTestSuite) TestRelativePathExistingOnlyInProcessDirectoryIsNotJoined() {
	processDir := s.T().TempDir()
	shellDir := s.T().TempDir()
	name := "spaced" + filepath.Ext(s.executable)
	relative := "." + string(filepath.Separator) + name + " dir" +
		string(filepath.Separator) + "provider"
	s.Require().NoError(os.MkdirAll(filepath.Join(processDir, name+" dir"), 0o750))
	s.copyTestBinary(filepath.Join(processDir, name+" dir", "provider"))
	s.copyTestBinary(filepath.Join(shellDir, name))
	s.T().Chdir(processDir)
	s.executablePaths["PROVIDER"] = relative

	output, err := s.run(
		"cd '"+shellDir+"'; ${PROVIDER} init",
		"PROVIDER="+relative,
		helperProcessEnv+"=1",
	)

	s.Require().NoError(err)
	s.Equal("helper ran with init", output)
}

func (s *ExecutablePathTestSuite) TestNonBreakingSpaceStaysInsideTheJoinedPath() {
	dir := filepath.Join(s.T().TempDir(), "Jane\u00a0Doe and co")
	s.Require().NoError(os.MkdirAll(dir, 0o750))
	executable := filepath.Join(dir, filepath.Base(s.executable))
	s.copyTestBinary(executable)
	s.executablePaths["PROVIDER"] = executable

	output, err := s.run(
		"${PROVIDER} init",
		"PROVIDER="+executable,
		helperProcessEnv+"=1",
	)

	s.Require().NoError(err)
	s.Equal("helper ran with init", output)
}

func (s *ExecutablePathTestSuite) TestVariableArgumentWithSpacesStillSplits() {
	output, err := s.run("echo ${ARGS}", "ARGS=a b")

	s.Require().NoError(err)
	s.Equal("a b\n", output)
}

func (s *ExecutablePathTestSuite) TestSourcedLiteralCommandKeepsItsOwnExecutable() {
	dir := s.T().TempDir()
	ext := filepath.Ext(s.executable)
	tool := filepath.Join(dir, "tool"+ext)
	spacedTool := tool + " status" + ext
	s.copyTestBinary(tool)
	s.copyTestBinary(spacedTool)
	source := filepath.Join(dir, "source.sh")
	s.Require().NoError(os.WriteFile(source, []byte("'"+tool+"' status"+ext+"\n"), 0o600))
	s.executablePaths["CMD"] = spacedTool

	output, err := s.run(
		"$CMD first; source '"+source+"'",
		"CMD="+spacedTool,
		helperProcessEnv+"=1",
	)

	s.Require().NoError(err)
	s.Equal("helper ran with firsthelper ran with status"+ext, output)
}

func (s *ExecutablePathTestSuite) TestGenericShellDoesNotReinterpretSpacedFile() {
	dir := s.T().TempDir()
	ext := filepath.Ext(s.executable)
	tool := filepath.Join(dir, "tool"+ext)
	spacedTool := tool + " status" + ext
	s.copyTestBinary(tool)
	s.copyTestBinary(spacedTool)
	s.executablePaths = nil

	output, err := s.run(
		"$CMD init",
		"CMD="+spacedTool,
		helperProcessEnv+"=1",
	)

	s.Require().NoError(err)
	s.Equal("helper ran with init", output)
}

func (s *ExecutablePathTestSuite) TestProviderVariableReassignedToNonExecutableFileStillSplits() {
	dir := s.T().TempDir()
	ext := filepath.Ext(s.executable)
	tool := filepath.Join(dir, "tool"+ext)
	spacedTool := tool + " status" + ext
	s.copyTestBinary(tool)
	s.Require().NoError(os.WriteFile(spacedTool, []byte("not executable"), 0o600))

	output, err := s.run(
		"CMD='"+spacedTool+"'; $CMD init",
		"CMD="+s.executable,
		helperProcessEnv+"=1",
	)

	s.Require().NoError(err)
	s.Equal("helper ran with init", output)
}

func (s *ExecutablePathTestSuite) TestEmptyProviderVariableDoesNotRunMarker() {
	output, err := s.run(
		"CMD=''; $CMD; echo done",
		"CMD="+s.executable,
	)

	s.Require().NoError(err)
	s.Equal("done\n", output)
}

func (s *ExecutablePathTestSuite) run(command string, environ ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	err := RunEmulatedShell(context.Background(), &CommandRunner{
		Command:         command,
		Stdout:          &stdout,
		Stderr:          &stderr,
		Environ:         append(os.Environ(), environ...),
		ExecutablePaths: s.executablePaths,
	})
	return stdout.String(), err
}

func (s *ExecutablePathTestSuite) copyTestBinary(target string) {
	self, err := os.Executable()
	s.Require().NoError(err)
	sourceRoot, err := os.OpenRoot(filepath.Dir(self))
	s.Require().NoError(err)
	defer func() { _ = sourceRoot.Close() }()
	source, err := sourceRoot.Open(filepath.Base(self))
	s.Require().NoError(err)
	defer func() { _ = source.Close() }()
	targetRoot, err := os.OpenRoot(filepath.Dir(target))
	s.Require().NoError(err)
	defer func() { _ = targetRoot.Close() }()
	file, err := targetRoot.OpenFile(filepath.Base(target), os.O_CREATE|os.O_WRONLY, 0o700)
	s.Require().NoError(err)
	_, err = io.Copy(file, source)
	s.Require().NoError(err)
	s.Require().NoError(file.Close())
}
