package dockercredentials

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/docker/docker-credential-helpers/credentials"
	"github.com/stretchr/testify/suite"
)

const testRegistry = "docker.io"

type HelperTestSuite struct {
	suite.Suite
}

func TestHelperSuite(t *testing.T) {
	suite.Run(t, new(HelperTestSuite))
}

func (s *HelperTestSuite) TestGet_Success() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Equal(http.MethodPost, r.Method)
		s.Equal("/docker-credentials", r.URL.Path)

		request := &Request{}
		err := json.NewDecoder(r.Body).Decode(request)
		s.NoError(err)
		s.Equal(testRegistry, request.ServerURL)

		_ = json.NewEncoder(w).Encode(&Credentials{
			ServerURL: testRegistry,
			Username:  "testuser",
			Secret:    "testpass", //nolint:gosec // test fixture
		})
	}))
	defer server.Close()

	helper := NewHelper(parsePort(server.URL))
	username, secret, err := helper.Get(testRegistry)

	s.NoError(err)
	s.Equal("testuser", username)
	s.Equal("testpass", secret)
}

func (s *HelperTestSuite) TestGet_EmptyCredentials() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	helper := NewHelper(parsePort(server.URL))
	username, secret, err := helper.Get(testRegistry)

	s.NoError(err)
	s.Empty(username)
	s.Empty(secret)
}

func (s *HelperTestSuite) TestGet_ServerError() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	helper := NewHelper(parsePort(server.URL))
	username, secret, err := helper.Get(testRegistry)

	s.NoError(err)
	s.Empty(username)
	s.Empty(secret)
}

func (s *HelperTestSuite) TestGet_WorkspaceServerFallback() {
	primaryServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}),
	)
	defer primaryServer.Close()

	workspaceServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			s.Equal(http.MethodPost, r.Method)
			s.Equal("/docker-credentials", r.URL.Path)

			request := &Request{}
			err := json.NewDecoder(r.Body).Decode(request)
			s.NoError(err)
			s.Equal(testRegistry, request.ServerURL)

			_ = json.NewEncoder(w).Encode(&Credentials{
				ServerURL: testRegistry,
				Username:  "workspaceuser",
				Secret:    "workspacepass", //nolint:gosec // test fixture
			})
		}),
	)
	defer workspaceServer.Close()

	_ = os.Setenv("DEVSY_WORKSPACE_CREDENTIALS_PORT", parsePortString(workspaceServer.URL))
	defer func() { _ = os.Unsetenv("DEVSY_WORKSPACE_CREDENTIALS_PORT") }()

	helper := NewHelper(parsePort(primaryServer.URL))
	username, secret, err := helper.Get(testRegistry)

	s.NoError(err)
	s.Equal("workspaceuser", username)
	s.Equal("workspacepass", secret)
}

func (s *HelperTestSuite) TestList_Success() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Equal(http.MethodPost, r.Method)
		s.Equal("/docker-credentials", r.URL.Path)

		request := &Request{}
		err := json.NewDecoder(r.Body).Decode(request)
		s.NoError(err)
		s.Equal("", request.ServerURL)

		_ = json.NewEncoder(w).Encode(&ListResponse{Registries: map[string]string{
			testRegistry: "testuser",
			"gcr.io":     "gcpuser",
		}})
	}))
	defer server.Close()

	helper := NewHelper(parsePort(server.URL))
	list, err := helper.List()

	s.NoError(err)
	s.Len(list, 2)
	s.Equal("testuser", list[testRegistry])
}

func (s *HelperTestSuite) TestList_Empty() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	helper := NewHelper(parsePort(server.URL))
	list, err := helper.List()

	s.NoError(err)
	s.Empty(list)
}

func (s *HelperTestSuite) TestAdd_NotSupported() {
	helper := NewHelper(0)
	err := helper.Add(&credentials.Credentials{})
	s.Error(err)
}

func (s *HelperTestSuite) TestDelete_NotSupported() {
	helper := NewHelper(0)
	err := helper.Delete(testRegistry)
	s.Error(err)
}

func (s *HelperTestSuite) TestSanitizeServerURL() {
	tests := []struct {
		input    string
		expected string
	}{
		{"https://docker.io", testRegistry},
		{"http://docker.io", testRegistry},
		{"docker.io/", testRegistry},
		{"https://docker.io/", testRegistry},
		{testRegistry, testRegistry},
	}

	for _, tt := range tests {
		result := sanitizeServerURL(tt.input)
		s.Equal(tt.expected, result)
	}
}

func parsePort(url string) int {
	var port int
	_, _ = fmt.Sscanf(url, "http://127.0.0.1:%d", &port)
	return port
}

func parsePortString(url string) string {
	return fmt.Sprintf("%d", parsePort(url))
}
