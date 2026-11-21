package platformclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/suite"
)

type ClientSuite struct {
	suite.Suite
}

func TestClientSuite(t *testing.T) {
	suite.Run(t, new(ClientSuite))
}

func (s *ClientSuite) TestNormalizeURL() {
	const local = "http://localhost:8180"
	tests := map[string]string{
		local:                         local,
		local + "/":                   local,
		"  https://gw.example.test//": "https://gw.example.test",
		"https://gw.example.test/a/":  "https://gw.example.test/a",
	}
	for raw, want := range tests {
		got, err := NormalizeURL(raw)
		s.Require().NoError(err, raw)
		s.Equal(want, got, raw)
	}
}

func (s *ClientSuite) TestNormalizeURLRejectsBadInput() {
	for _, raw := range []string{
		"", "localhost:8180", "ftp://x.test", "http://", "http://user:pw@x.test",
	} {
		_, err := NormalizeURL(raw)
		s.Require().Error(err, raw)
		s.NotContains(err.Error(), "pw@")
	}
}

func (s *ClientSuite) TestErrorEnvelopeBecomesAPIError() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(
			`{"error":{"code":"invalid_credentials","message":"invalid email or password"}}`,
		))
	}))
	defer srv.Close()

	_, err := New(srv.URL, nil).Login(context.Background(), "a@example.test", "pw")

	var apiErr *APIError
	s.Require().ErrorAs(err, &apiErr)
	s.Equal(http.StatusUnauthorized, apiErr.Status)
	s.Equal("invalid_credentials", apiErr.Code)
	s.Equal("invalid email or password", err.Error())
}

func (s *ClientSuite) TestNonEnvelopeErrorBodyIsNotEchoed() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>upstream said: secret-token-abc</html>"))
	}))
	defer srv.Close()

	_, err := New(srv.URL, nil).ListOrgs(context.Background(), "tok")

	s.Require().Error(err)
	s.NotContains(err.Error(), "secret-token-abc")
	s.Contains(err.Error(), "502")
}

func (s *ClientSuite) TestRedirectsAreNotFollowed() {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		followed.Store(true)
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()

	_, err := New(srv.URL, nil).Login(context.Background(), "a@example.test", "pw")

	s.Require().Error(err)
	s.False(followed.Load(), "login credentials must not be replayed to a redirect target")
}

func (s *ClientSuite) TestIncompleteKeyResponseIsAnError() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"1","key_id":"k"}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, nil).CreateAPIKey(
		context.Background(),
		"tok",
		CreateKeyRequest{OrgID: "org", Name: "n", Scopes: []string{"read"}},
	)

	s.Require().Error(err)
}
