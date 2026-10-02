package platform

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/suite"
)

type versionSuite struct {
	suite.Suite
}

func TestVersionSuite(t *testing.T) {
	suite.Run(t, new(versionSuite))
}

func (s *versionSuite) TestGetPlatformVersionParsesBothFields() {
	url := s.serve(http.StatusOK, `{"version":"1.2.3","devsyVersion":"v0.4.5"}`)

	got, err := GetPlatformVersion(url)
	s.Require().NoError(err)
	s.Equal("1.2.3", got.Version)
	s.Equal("v0.4.5", got.DevsyVersion)
}

func (s *versionSuite) TestGetPlatformVersionOmitsAbsentFields() {
	url := s.serve(http.StatusOK, `{}`)

	got, err := GetPlatformVersion(url)
	s.Require().NoError(err)
	s.Empty(got.Version)
	s.Empty(got.DevsyVersion)
}

func (s *versionSuite) TestGetPlatformVersionReportsStatusAndBody() {
	url := s.serve(http.StatusForbidden, "nope")

	_, err := GetPlatformVersion(url)
	s.Require().Error(err)
	s.Contains(err.Error(), "nope")
	s.Contains(err.Error(), "403")
}

func (s *versionSuite) TestGetPlatformVersionRejectsMalformedJSON() {
	url := s.serve(http.StatusOK, "not json")

	_, err := GetPlatformVersion(url)
	s.Require().Error(err)
	s.Contains(err.Error(), "parse")
}

func (s *versionSuite) TestGetDevsyVersionAddsMissingVPrefix() {
	url := s.serve(http.StatusOK, `{"devsyVersion":"0.4.5"}`)

	got, err := GetDevsyVersion(url)
	s.Require().NoError(err)
	s.Equal("v0.4.5", got)
}

func (s *versionSuite) TestGetDevsyVersionKeepsExistingVPrefix() {
	url := s.serve(http.StatusOK, `{"devsyVersion":"v0.4.5"}`)

	got, err := GetDevsyVersion(url)
	s.Require().NoError(err)
	s.Equal("v0.4.5", got)
}

// The error must point at --version, since a missing devsyVersion is a
// provider-version problem.
func (s *versionSuite) TestGetDevsyVersionRequiresDevsyVersion() {
	url := s.serve(http.StatusOK, `{"version":"1.2.3"}`)

	_, err := GetDevsyVersion(url)
	s.Require().Error(err)
	s.Contains(err.Error(), "--version")
}

func (s *versionSuite) TestGetDevsyVersionPropagatesFetchError() {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	url := server.URL
	server.Close()

	_, err := GetDevsyVersion(url)
	s.Require().Error(err)
	s.Contains(err.Error(), "get")
}

func (s *versionSuite) serve(status int, body string) string {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	s.T().Cleanup(server.Close)
	return server.URL
}
