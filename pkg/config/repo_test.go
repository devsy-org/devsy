package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/suite"
)

type RepoTestSuite struct {
	suite.Suite
	originalEnv string
}

func (s *RepoTestSuite) SetupTest() {
	s.originalEnv = os.Getenv(EnvAgentURL)
}

func (s *RepoTestSuite) TearDownTest() {
	if s.originalEnv != "" {
		_ = os.Setenv(EnvAgentURL, s.originalEnv)
	} else {
		_ = os.Unsetenv(EnvAgentURL)
	}
}

func (s *RepoTestSuite) TestDefaultAgentDownloadURL_NoTrailingSlash() {
	_ = os.Setenv(EnvAgentURL, "https://example.com/releases/latest/download")
	s.Equal("https://example.com/releases/latest/download", DefaultAgentDownloadURL())
}

func (s *RepoTestSuite) TestDefaultAgentDownloadURL_SingleTrailingSlash() {
	_ = os.Setenv(EnvAgentURL, "https://example.com/releases/latest/download/")
	s.Equal("https://example.com/releases/latest/download", DefaultAgentDownloadURL())
}

func (s *RepoTestSuite) TestDefaultAgentDownloadURL_MultipleTrailingSlashes() {
	_ = os.Setenv(EnvAgentURL, "https://example.com/releases/latest/download///")
	s.Equal("https://example.com/releases/latest/download", DefaultAgentDownloadURL())
}

func TestRepoSuite(t *testing.T) {
	suite.Run(t, new(RepoTestSuite))
}
