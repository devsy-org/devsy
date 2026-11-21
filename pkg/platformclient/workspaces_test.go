package platformclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/suite"
)

const wsTestName = "ws-a"

const wsTestKey = "test-api-key-placeholder"

type WorkspacesSuite struct {
	suite.Suite
}

func TestWorkspacesSuite(t *testing.T) {
	suite.Run(t, new(WorkspacesSuite))
}

func (s *WorkspacesSuite) TestListSendsKeyAndMapsRows() {
	var method, path, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		_, _ = w.Write([]byte(
			`[{"name":"` + wsTestName + `","phase":"Running"},{"name":"ws-b","phase":"Pending"}]`,
		))
	}))
	defer srv.Close()

	got, err := New(srv.URL, nil).ListWorkspaces(context.Background(), wsTestKey)

	s.Require().NoError(err)
	s.Equal(http.MethodGet, method)
	s.Equal("/v1/workspaces", path)
	s.Equal("Bearer "+wsTestKey, auth)
	s.Equal(
		[]Workspace{{Name: wsTestName, Phase: "Running"}, {Name: "ws-b", Phase: "Pending"}},
		got,
	)
}

func (s *WorkspacesSuite) TestListOfNothingIsEmpty() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, nil).ListWorkspaces(context.Background(), wsTestKey)

	s.Require().NoError(err)
	s.Empty(got)
}

func (s *WorkspacesSuite) TestCreatePostsNameAndTemplateOnly() {
	var method, path, auth, contentType string
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		auth, contentType = r.Header.Get("Authorization"), r.Header.Get("Content-Type")
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"name":"` + wsTestName + `","phase":"Pending"}`))
	}))
	defer srv.Close()

	got, err := New(
		srv.URL,
		nil,
	).CreateWorkspace(context.Background(), wsTestKey, wsTestName, "tmpl")

	s.Require().NoError(err)
	s.Equal(http.MethodPost, method)
	s.Equal("/v1/workspaces", path)
	s.Equal("Bearer "+wsTestKey, auth)
	s.Equal("application/json", contentType)
	var decoded map[string]string
	s.Require().NoError(json.Unmarshal(body, &decoded))
	s.Equal(map[string]string{"name": wsTestName, "template": "tmpl"}, decoded)
	s.Equal(Workspace{Name: wsTestName, Phase: "Pending"}, got)
}

func (s *WorkspacesSuite) TestUnauthorizedSuggestsLogin() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"invalid api key"}}`))
	}))
	defer srv.Close()
	client := New(srv.URL, nil)

	_, listErr := client.ListWorkspaces(context.Background(), wsTestKey)
	_, createErr := client.CreateWorkspace(context.Background(), wsTestKey, wsTestName, "tmpl")

	for _, err := range []error{listErr, createErr} {
		var authErr *AuthError
		s.Require().ErrorAs(err, &authErr)
		s.Contains(err.Error(), "devsy login")
		s.Contains(err.Error(), "authentication")
		var apiErr *APIError
		s.Require().ErrorAs(err, &apiErr)
		s.Equal(http.StatusUnauthorized, apiErr.Status)
		s.NotContains(err.Error(), wsTestKey)
	}
}

func (s *WorkspacesSuite) TestForbiddenIsAPermissionError() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"insufficient scope"}}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, nil).CreateWorkspace(context.Background(), wsTestKey, wsTestName, "tmpl")

	s.Require().Error(err)
	s.Contains(err.Error(), "permission denied")
	s.Contains(err.Error(), "stored API key lacks write scope")
	s.Contains(err.Error(), "insufficient scope")
	s.NotContains(err.Error(), wsTestKey)
	var permErr *PermissionError
	s.Require().ErrorAs(err, &permErr)
	s.Equal("write", permErr.Scope)
	var authErr *AuthError
	s.False(errors.As(err, &authErr), "a 403 is not an authentication failure")
}

func (s *WorkspacesSuite) TestForbiddenListNamesTheReadScope() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"insufficient scope"}}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, nil).ListWorkspaces(context.Background(), wsTestKey)

	s.Require().Error(err)
	s.Contains(err.Error(), "stored API key lacks read scope")
}

func (s *WorkspacesSuite) TestOtherErrorsKeepTheGatewayMessage() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"conflict","message":"workspace already exists"}}`))
	}))
	defer srv.Close()

	_, err := New(srv.URL, nil).CreateWorkspace(context.Background(), wsTestKey, wsTestName, "tmpl")

	s.Require().Error(err)
	s.Equal("workspace already exists", err.Error())
}

type lifecycleCall func(*Client, context.Context, string, string) (Workspace, error)

func lifecycleCalls() map[string]struct {
	method, path string
	call         lifecycleCall
} {
	return map[string]struct {
		method, path string
		call         lifecycleCall
	}{
		"sleep": {
			http.MethodPost, "/v1/workspaces/" + wsTestName + "/sleep",
			(*Client).SleepWorkspace,
		},
		"wake": {
			http.MethodPost, "/v1/workspaces/" + wsTestName + "/wake",
			(*Client).WakeWorkspace,
		},
		"delete": {
			http.MethodDelete, "/v1/workspaces/" + wsTestName,
			(*Client).DeleteWorkspace,
		},
	}
}

func (s *WorkspacesSuite) TestLifecycleCallsUseTheirRouteAndKeyAndAcceptA202() {
	for name, tc := range lifecycleCalls() {
		s.Run(name, func() {
			var method, path, auth string
			var body []byte
			srv := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					method, path, auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
					body, _ = io.ReadAll(r.Body)
					w.WriteHeader(http.StatusAccepted)
					_, _ = w.Write([]byte(`{"name":"` + wsTestName + `","phase":"Deleting"}`))
				}),
			)
			defer srv.Close()

			got, err := tc.call(New(srv.URL, nil), context.Background(), wsTestKey, wsTestName)

			s.Require().NoError(err)
			s.Equal(tc.method, method)
			s.Equal(tc.path, path)
			s.Equal("Bearer "+wsTestKey, auth)
			s.Empty(body)
			s.Equal(Workspace{Name: wsTestName, Phase: "Deleting"}, got)
		})
	}
}

func (s *WorkspacesSuite) TestLifecycleCallsEscapeTheName() {
	var rawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	_, err := New(srv.URL, nil).SleepWorkspace(context.Background(), wsTestKey, "a/b")

	s.Require().NoError(err)
	s.Equal("/v1/workspaces/a%2Fb/sleep", rawPath)
}

func (s *WorkspacesSuite) TestLifecycleCallsRejectASuccessThatIsNot202() {
	for name, tc := range lifecycleCalls() {
		s.Run(name, func() {
			srv := httptest.NewServer(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"name":"` + wsTestName + `","phase":"Running"}`))
				}),
			)
			defer srv.Close()

			_, err := tc.call(New(srv.URL, nil), context.Background(), wsTestKey, wsTestName)

			s.Require().Error(err)
			s.Contains(err.Error(), "200")
		})
	}
}

func (s *WorkspacesSuite) TestLifecycleCallsKeepTheGatewayErrorMessage() {
	cases := []struct {
		status  int
		message string
		want    string
	}{
		{http.StatusNotFound, "workspace not found", "workspace not found"},
		{http.StatusForbidden, "insufficient scope", "stored API key lacks write scope"},
		{http.StatusUnauthorized, "invalid api key", "devsy login"},
	}
	for name, tc := range lifecycleCalls() {
		for _, c := range cases {
			s.Run(name+"/"+http.StatusText(c.status), func() {
				srv := httptest.NewServer(
					http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						w.WriteHeader(c.status)
						_, _ = w.Write(
							[]byte(`{"error":{"code":"x","message":"` + c.message + `"}}`),
						)
					}),
				)
				defer srv.Close()

				_, err := tc.call(New(srv.URL, nil), context.Background(), wsTestKey, wsTestName)

				s.Require().Error(err)
				s.Contains(err.Error(), c.want)
				s.NotContains(err.Error(), wsTestKey)
			})
		}
	}
}

func (s *WorkspacesSuite) TestLifecycleCallsWithAnEmptyKeyFailBeforeCallingTheGateway() {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer srv.Close()

	for _, tc := range lifecycleCalls() {
		_, err := tc.call(New(srv.URL, nil), context.Background(), "", wsTestName)
		s.Require().Error(err)
		s.Contains(err.Error(), "devsy login")
	}
	s.False(called)
}

func (s *WorkspacesSuite) TestEmptyKeyFailsBeforeCallingTheGateway() {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer srv.Close()

	_, err := New(srv.URL, nil).ListWorkspaces(context.Background(), "")

	s.Require().Error(err)
	s.Contains(err.Error(), "devsy login")
	s.False(called)
}
