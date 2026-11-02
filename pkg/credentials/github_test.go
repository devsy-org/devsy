package credentials

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type githubCredentialsClient struct {
	*mockCredentialsClient
	calls atomic.Int32
	code  codes.Code
}

func (c *githubCredentialsClient) Ping(
	context.Context,
	*tunnel.Empty,
	...grpc.CallOption,
) (*tunnel.Empty, error) {
	if c.code != codes.OK {
		return nil, status.Error(c.code, "unavailable fixture transport")
	}
	return &tunnel.Empty{}, nil
}

func TestReadinessRejectsDeadTransportWithoutCredentialLookup(t *testing.T) {
	client := &githubCredentialsClient{code: codes.Unavailable}
	h := newPreparedHandler(context.Background(), client, "vscode", readyFixture())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost/ready", nil))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Zero(t, client.calls.Load())
}

func TestCredentialPostPinsVerifiedHostAndInstanceBeforeRPC(t *testing.T) {
	client := &githubCredentialsClient{}
	identity := readyFixture()
	h := newPreparedHandler(context.Background(), client, identity.Owner, identity)
	for _, pair := range [][2]string{{"", ""}, {"old-instance", identity.HostOwner}, {identity.Instance, "other-host"}} {
		r := httptest.NewRequest(http.MethodPost, "http://localhost/github-credentials", nil)
		r.Header.Set(HeaderCredentialsInstance, pair[0])
		r.Header.Set(HeaderCredentialsHostOwner, pair[1])
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		require.Equal(t, http.StatusConflict, w.Code)
		require.JSONEq(t, `{"status":"relay_changed"}`, w.Body.String())
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	}
	require.Zero(t, client.calls.Load())
	r := httptest.NewRequest(http.MethodPost, "http://localhost/github-credentials", nil)
	r.Header.Set(HeaderCredentialsInstance, identity.Instance)
	r.Header.Set(HeaderCredentialsHostOwner, identity.HostOwner)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.EqualValues(t, 1, client.calls.Load())
}

func (c *githubCredentialsClient) GitHubCredentials(
	context.Context,
	*tunnel.Empty,
	...grpc.CallOption,
) (*tunnel.Message, error) {
	c.calls.Add(1)
	if c.code != codes.OK {
		return nil, status.Error(c.code, "must not expose raw RPC details")
	}
	return &tunnel.Message{Message: `{"status":"no_active_accounts"}`}, nil
}

func TestGitHubHTTPBoundaryMethodsStatusesAndHeaders(t *testing.T) {
	for _, tc := range []struct {
		code codes.Code
		want string
	}{
		{codes.OK, "no_active_accounts"},
		{codes.Unimplemented, "rpc_unavailable"},
		{codes.PermissionDenied, "rpc_denied"},
		{codes.DeadlineExceeded, "rpc_timeout"},
		{codes.Canceled, "rpc_cancelled"},
		{codes.Internal, "rpc_error"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			client := &githubCredentialsClient{code: tc.code}
			h := newCredentialsHandler(context.Background(), client, "guest")
			get := httptest.NewRecorder()
			h.ServeHTTP(
				get,
				httptest.NewRequest(http.MethodGet, "http://localhost/github-credentials", nil),
			)
			require.Equal(t, http.StatusMethodNotAllowed, get.Code)
			require.Zero(t, client.calls.Load())
			post := httptest.NewRecorder()
			h.ServeHTTP(
				post,
				httptest.NewRequest(http.MethodPost, "http://localhost/github-credentials", nil),
			)
			require.Equal(t, http.StatusOK, post.Code)
			require.Equal(t, "application/json", post.Header().Get("Content-Type"))
			require.Equal(t, "no-store", post.Header().Get("Cache-Control"))
			var body map[string]string
			require.NoError(t, json.Unmarshal(post.Body.Bytes(), &body))
			require.Equal(t, tc.want, body["status"])
			require.EqualValues(t, 1, client.calls.Load())
		})
	}
}

func TestGitHubHTTPMissingOptionalMethod(t *testing.T) {
	h := newCredentialsHandler(context.Background(), &mockCredentialsClient{}, "guest")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "http://localhost/github-credentials", nil))
	require.JSONEq(t, `{"status":"rpc_unavailable"}`, w.Body.String())
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
}
