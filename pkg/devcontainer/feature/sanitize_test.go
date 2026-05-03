package feature

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type SanitizeTestSuite struct {
	suite.Suite
}

func TestSanitizeTestSuite(t *testing.T) {
	suite.Run(t, new(SanitizeTestSuite))
}

func (s *SanitizeTestSuite) TestStripsPath() {
	result := sanitizeURL("https://ghcr.io/devcontainers/features/go:1.2.3")
	s.Equal("ghcr.io", result)
}

func (s *SanitizeTestSuite) TestStripsPathWithPort() {
	result := sanitizeURL("https://registry.example.com:5000/org/repo/image:latest")
	s.Equal("registry.example.com", result)
}

func (s *SanitizeTestSuite) TestHostnameOnly() {
	result := sanitizeURL("https://ghcr.io")
	s.Equal("ghcr.io", result)
}

func (s *SanitizeTestSuite) TestEmptyString() {
	result := sanitizeURL("")
	s.Equal("", result)
}

func (s *SanitizeTestSuite) TestNoScheme() {
	result := sanitizeURL("ghcr.io/devcontainers/features/go")
	s.Equal("ghcr.io/devcontainers/features/go", result)
}

func (s *SanitizeTestSuite) TestHTTP() {
	result := sanitizeURL("http://my-registry.local/v2/feature")
	s.Equal("my-registry.local", result)
}

func (s *SanitizeTestSuite) TestMalformedURL() {
	result := sanitizeURL("://broken")
	s.Equal("://broken", result)
}
