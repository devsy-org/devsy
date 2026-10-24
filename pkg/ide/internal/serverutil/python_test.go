package serverutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/suite"
)

const (
	missingPythonExecutable = "devsy-missing-python-tool"
	fakeUvArgsFile          = "args"
)

type PythonToolSuite struct {
	suite.Suite
}

func TestPythonToolSuite(t *testing.T) {
	suite.Run(t, new(PythonToolSuite))
}

func (s *PythonToolSuite) SetupTest() {
	if runtime.GOOS == goosWindows {
		s.T().Skip("uses sh")
	}
}

// useFakeUv puts a uv stub first on PATH. The stub records its argv in
// fakeUvArgsFile next to itself and then runs body.
func (s *PythonToolSuite) useFakeUv(body string) string {
	dir := s.T().TempDir()
	script := "#!/bin/sh\necho \"$@\" >> \"$(dirname \"$0\")/" + fakeUvArgsFile + "\"\n" + body + "\n"
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "uv"), []byte(script), 0o700))
	s.T().Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func (s *PythonToolSuite) TestInstallSkippedWhenExecutableExists() {
	dir := s.useFakeUv("exit 1")

	err := InstallPythonTool("", PythonTool{
		Name:       "shell",
		Package:    "unused",
		Executable: "sh",
	})
	s.NoError(err)
	s.NoFileExists(filepath.Join(dir, fakeUvArgsFile))
}

func (s *PythonToolSuite) TestInstallRunsPackageInstallWithSelectedTool() {
	dir := s.useFakeUv("exit 0")

	err := InstallPythonTool("", PythonTool{
		Name:       "jupyter notebook",
		Package:    "notebook",
		Executable: missingPythonExecutable,
	})
	s.Require().NoError(err)

	args, err := os.ReadFile(filepath.Join(dir, fakeUvArgsFile))
	s.Require().NoError(err)
	s.Equal("tool install notebook\n", string(args))
}

func (s *PythonToolSuite) TestInstallFailureWrapsOutputWithDisplayName() {
	s.useFakeUv("echo fake-uv-failure; exit 3")

	err := InstallPythonTool("", PythonTool{
		Name:       "marimo",
		Package:    "marimo",
		Executable: missingPythonExecutable,
	})
	s.Require().Error(err)
	s.Contains(err.Error(), "error installing marimo: ")
	s.Contains(err.Error(), "fake-uv-failure")
}
