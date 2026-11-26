package runenv_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/e2e/framework/runenv"
	"github.com/devsy-org/devsy/e2e/framework/runenv/runenvtest"
	"github.com/stretchr/testify/suite"
)

type GitHubFilesSuite struct{ suite.Suite }

func TestGitHubFilesSuite(t *testing.T) { suite.Run(t, new(GitHubFilesSuite)) }

func (s *GitHubFilesSuite) TestAppendsEnvAndPath() {
	files := runenvtest.NewMemFiles()
	g := runenv.GitHubFiles{EnvPath: ghEnvPath, PathPath: ghPathPath, Files: files}

	s.Require().NoError(g.SetEnv("DOCKER_SSH_HOST", "ssh://runner@127.0.0.1:2222"))
	s.Require().NoError(g.SetEnv("EMPTY", ""))
	s.Require().NoError(g.AddPath(`C:\tools\docker`))

	s.Equal("DOCKER_SSH_HOST=ssh://runner@127.0.0.1:2222\nEMPTY=\n", string(files.Data[ghEnvPath]))
	s.Equal("C:\\tools\\docker\n", string(files.Data[ghPathPath]))
	s.EqualValues(0o600, files.Modes[ghEnvPath])
}

func (s *GitHubFilesSuite) TestRejectsBadInput() {
	files := runenvtest.NewMemFiles()
	g := runenv.GitHubFiles{EnvPath: ghEnvPath, PathPath: ghPathPath, Files: files}

	for _, name := range []string{"", "1BAD", "A-B", "A B", "A=B", "A\nB", "é"} {
		s.Require().Error(g.SetEnv(name, "v"), "name %q", name)
	}
	for _, value := range []string{"a\nB=injected", "a\rb", "a\x00b"} {
		err := g.SetEnv("OK", value)
		se, ok := errors.AsType[*runenv.StageError](err)
		s.Require().True(ok)
		s.Equal(runenv.CodeConfigInvalid, se.Code)
	}
	s.Require().Error(g.AddPath("dir\nC:\\evil"))
	s.Require().Error(g.AddPath(""))
	s.Empty(files.Data, "nothing written for rejected input")
}

func (s *GitHubFilesSuite) TestLocalModePrintsRedacted() {
	var out bytes.Buffer
	g := runenv.GitHubFiles{Files: runenvtest.NewMemFiles(), Out: &out}

	s.Require().NoError(g.SetEnv("DOCKER_HOST", "ssh://x"))
	s.Require().NoError(g.SetEnv("GH_ACCESS_TOKEN", "hunter22secret"))
	s.Require().NoError(g.AddPath("/opt/bin"))

	s.Contains(out.String(), "local env: DOCKER_HOST=ssh://x\n")
	s.Contains(out.String(), "local path: /opt/bin\n")
	s.NotContains(out.String(), "hunter22secret")
	s.Empty(g.Files.(*runenvtest.MemFiles).Data)
}

func (s *GitHubFilesSuite) TestAppendFailureIsReported() {
	files := runenvtest.NewMemFiles()
	files.FailAppend = errors.New("disk full")
	g := runenv.GitHubFiles{EnvPath: ghEnvPath, Files: files}
	s.Require().ErrorContains(g.SetEnv("A", "b"), "disk full")
}

var (
	ghEnvPath  = filepath.FromSlash("/gh/env")
	ghPathPath = filepath.FromSlash("/gh/path")
)
