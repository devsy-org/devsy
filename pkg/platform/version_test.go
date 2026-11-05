package platform

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	devsyhttp "github.com/devsy-org/devsy/pkg/http"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type versionRoundTripper func(*http.Request) (*http.Response, error)

func (f versionRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type versionBody struct {
	io.Reader
	closed bool
}

func (b *versionBody) Close() error {
	b.closed = true
	return nil
}

type versionFailReader struct{}

func (versionFailReader) Read([]byte) (int, error) {
	return 0, errors.New("body read failed")
}

func TestGetPlatformVersionClosesBody(t *testing.T) {
	client := devsyhttp.GetHTTPClient()
	originalTransport := client.Transport
	t.Cleanup(func() { client.Transport = originalTransport })

	tests := []struct {
		name    string
		status  int
		reader  io.Reader
		wantErr bool
	}{
		{name: "success", status: http.StatusOK, reader: strings.NewReader(`{"version":"1.2.3"}`)},
		{
			name:    "status",
			status:  http.StatusForbidden,
			reader:  strings.NewReader("nope"),
			wantErr: true,
		},
		{name: "read", status: http.StatusOK, reader: versionFailReader{}, wantErr: true},
		{name: "parse", status: http.StatusOK, reader: strings.NewReader("invalid"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &versionBody{Reader: tt.reader}
			client.Transport = versionRoundTripper(
				func(*http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: tt.status,
						Body:       body,
						Header:     make(http.Header),
					}, nil
				},
			)
			_, err := GetPlatformVersion("http://example.test")
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.True(t, body.closed, "response body was not closed")
		})
	}
}

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
