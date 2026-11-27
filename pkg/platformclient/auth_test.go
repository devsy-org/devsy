package platformclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type AuthSuite struct {
	suite.Suite
}

func TestAuthSuite(t *testing.T) {
	suite.Run(t, new(AuthSuite))
}

func (s *AuthSuite) TestLoginSendsCredentialsAndDecodesSession() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Equal(http.MethodPost, r.Method)
		s.Equal("/v1/auth/login", r.URL.Path)
		s.Empty(r.Header.Get("Authorization"))
		var body map[string]string
		s.Require().NoError(json.NewDecoder(r.Body).Decode(&body))
		s.Equal(map[string]string{"email": "a@example.test", "password": "pw"}, body)
		_, _ = w.Write([]byte(
			`{"token":"tok","expires_at":"2030-01-02T03:04:05Z","user_id":"u1"}`,
		))
	}))
	defer srv.Close()

	session, err := New(srv.URL, nil).Login(context.Background(), "a@example.test", "pw")

	s.Require().NoError(err)
	s.Equal("tok", session.Token)
	s.Equal("u1", session.UserID)
	s.True(session.ExpiresAt.Equal(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)))
}

func (s *AuthSuite) TestLoginWithoutTokenIsAnError() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"user_id":"u1"}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, nil).Login(context.Background(), "a@example.test", "pw")

	s.Require().Error(err)
}

func (s *AuthSuite) TestLogoutSendsBearerToken() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Equal(http.MethodPost, r.Method)
		s.Equal("/v1/auth/logout", r.URL.Path)
		s.Equal("Bearer tok", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s.Require().NoError(New(srv.URL, nil).Logout(context.Background(), "tok"))
}

func (s *AuthSuite) TestLogoutWithExpiredSessionIsAnAPIError() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"invalid session"}}`))
	}))
	defer srv.Close()

	err := New(srv.URL, nil).Logout(context.Background(), "old")

	var apiErr *APIError
	s.Require().ErrorAs(err, &apiErr)
	s.Equal(http.StatusUnauthorized, apiErr.Status)
}

func (s *AuthSuite) TestListOrgsFollowsCursors() {
	var cursors []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Equal("/v1/orgs", r.URL.Path)
		s.Equal("Bearer tok", r.Header.Get("Authorization"))
		s.Equal("100", r.URL.Query().Get("limit"))
		cursor := r.URL.Query().Get("cursor")
		cursors = append(cursors, cursor)
		switch cursor {
		case "":
			_, _ = w.Write([]byte(
				`{"orgs":[{"id":"o1","name":"one","role":"owner"}],"next_cursor":"c2"}`,
			))
		default:
			_, _ = w.Write([]byte(`{"orgs":[{"id":"o2","name":"two","role":"member"}]}`))
		}
	}))
	defer srv.Close()

	orgs, err := New(srv.URL, nil).ListOrgs(context.Background(), "tok")

	s.Require().NoError(err)
	s.Equal([]string{"", "c2"}, cursors)
	s.Equal([]Org{
		{ID: "o1", Name: "one", Role: "owner"},
		{ID: "o2", Name: "two", Role: "member"},
	}, orgs)
}

func (s *AuthSuite) TestListOrgsEncodesCursor() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			_, _ = w.Write([]byte(`{"orgs":[],"next_cursor":"a b&c=d"}`))
			return
		}
		s.Equal("a b&c=d", r.URL.Query().Get("cursor"))
		_, _ = w.Write([]byte(`{"orgs":[]}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, nil).ListOrgs(context.Background(), "tok")

	s.Require().NoError(err)
}

func (s *AuthSuite) TestListOrgsStopsAfterTooManyPages() {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		pages++
		cursor := strconv.Quote("c" + strconv.Itoa(pages))
		_, _ = fmt.Fprintf(w, `{"orgs":[],"next_cursor":%s}`, cursor)
	}))
	defer srv.Close()

	_, err := New(srv.URL, nil).ListOrgs(context.Background(), "tok")

	s.Require().Error(err)
	s.Equal(maxOrgPages, pages)
}

func (s *AuthSuite) TestRateLimitCarriesRetryAfter() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"slow down"}}`))
	}))
	defer srv.Close()

	err := New(srv.URL, nil).Logout(context.Background(), "tok")

	var apiErr *APIError
	s.Require().ErrorAs(err, &apiErr)
	s.Equal("rate_limited", apiErr.Code)
	s.Equal(3*time.Second, apiErr.RetryAfter)
	s.False(IsUnauthorized(err))
}

func (s *AuthSuite) TestIsUnauthorized() {
	s.True(IsUnauthorized(fmt.Errorf("wrapped: %w", &APIError{Status: http.StatusUnauthorized})))
	s.False(IsUnauthorized(&APIError{Status: http.StatusForbidden}))
	s.False(IsUnauthorized(nil))
}
