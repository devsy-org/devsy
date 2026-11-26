package platformclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type SafetySuite struct {
	suite.Suite
}

func TestSafetySuite(t *testing.T) {
	suite.Run(t, new(SafetySuite))
}

func (s *SafetySuite) TestRetryAfterIsCapped() {
	s.Equal(maxRetryAfter, retryAfter("9223372037"))
	s.Equal(maxRetryAfter, retryAfter("3600"))
	s.Equal(time.Duration(0), retryAfter("99999999999999999999"))
	s.Equal(time.Duration(0), retryAfter("-5"))
	s.Equal(7*time.Second, retryAfter(" 7 "))
}

func (s *SafetySuite) TestErrorTextIsPrintable() {
	body, err := json.Marshal(map[string]any{"error": map[string]string{
		"code":    "bad\x1b[2Jcode",
		"message": "line1\r\nline2\x07 " + strings.Repeat("x", 500),
	}})
	s.Require().NoError(err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	err = New(srv.URL, nil).Logout(context.Background(), "tok")

	var apiErr *APIError
	s.Require().ErrorAs(err, &apiErr)
	s.NotContains(apiErr.Code, "\x1b")
	s.True(strings.HasPrefix(apiErr.Message, "line1 line2 x"))
	s.LessOrEqual(len([]rune(apiErr.Message)), maxErrorText)
	for _, r := range apiErr.Message + apiErr.Code {
		s.GreaterOrEqual(r, ' ')
	}
}

func (s *SafetySuite) TestNormalizeURLAllowsPlainHTTPOnlyForLoopback() {
	for _, raw := range []string{
		"http://localhost:8080", "http://LOCALHOST", "http://127.0.0.1:1", "http://[::1]:80",
		testGateway,
	} {
		_, err := NormalizeURL(raw)
		s.Require().NoError(err, raw)
	}
	for _, raw := range []string{
		"http://gw.example.test", "http://10.0.0.5:8080", "http://localhost.example.test",
	} {
		_, err := NormalizeURL(raw)
		s.Require().ErrorContains(err, "https", raw)
	}
}

func (s *SafetySuite) TestNormalizeURLDropsEmptyQueryAndFragment() {
	for raw, want := range map[string]string{
		"http://localhost:1/?":        "http://localhost:1",
		"https://gw.example.test/a?#": "https://gw.example.test/a",
		"https://gw.example.test/#x":  testGateway,
	} {
		got, err := NormalizeURL(raw)
		s.Require().NoError(err, raw)
		s.Equal(want, got, raw)
	}
}

func (s *SafetySuite) TestNormalizeURLErrorsDoNotEchoInput() {
	for _, raw := range []string{
		"ftp://u:secretpw@h/", "http://u:secretpw@h:bad/", "https://u:secretpw@h/",
		"secretpw", "http://secretpw@",
	} {
		_, err := NormalizeURL(raw)
		s.Require().Error(err, raw)
		s.NotContains(err.Error(), "secretpw", raw)
	}
}

func (s *SafetySuite) TestSuppliedClientNeverFollowsRedirects() {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		followed.Store(true)
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer srv.Close()
	own := &http.Client{Timeout: time.Minute}

	_, err := New(srv.URL, own).Login(context.Background(), "a@example.test", "pw")

	s.Require().Error(err)
	s.False(followed.Load(), "the password body must not be replayed to a redirect target")
	s.Nil(own.CheckRedirect, "the caller's client must not be modified")
}

func (s *SafetySuite) TestEmptyTokenFailsBeforeAnyRequest() {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer srv.Close()
	c := New(srv.URL, nil)

	s.Require().ErrorIs(c.Logout(context.Background(), ""), errTokenRequired)
	_, err := c.ListOrgs(context.Background(), "")
	s.Require().ErrorIs(err, errTokenRequired)
	s.Zero(calls.Load())
}

func (s *SafetySuite) TestLoginWithoutExpiryIsAnError() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"token":"tok","user_id":"u1"}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, nil).Login(context.Background(), "a@example.test", "pw")

	s.Require().Error(err)
}

func (s *SafetySuite) TestOversizedResponseIsAnError() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"orgs":[],"pad":"` + strings.Repeat("x", maxResponseBytes) + `"}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, nil).ListOrgs(context.Background(), "tok")

	s.Require().ErrorIs(err, errResponseTooBig)
}

func (s *SafetySuite) TestListOrgsRejectsRepeatedCursor() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"orgs":[],"next_cursor":"same"}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, nil).ListOrgs(context.Background(), "tok")

	s.Require().ErrorIs(err, errUnexpectedPages)
}

func (s *SafetySuite) TestListOrgsRejectsOversizedPage() {
	orgs := make([]Org, orgPageSize+1)
	body, err := json.Marshal(orgPage{Orgs: orgs})
	s.Require().NoError(err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	_, err = New(srv.URL, nil).ListOrgs(context.Background(), "tok")

	s.Require().ErrorIs(err, errUnexpectedPages)
}
