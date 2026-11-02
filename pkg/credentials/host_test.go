package credentials

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type hostGuardClient struct {
	githubCredentialsClient
	pings atomic.Int32
}

func (c *hostGuardClient) Ping(
	context.Context,
	*tunnel.Empty,
	...grpc.CallOption,
) (*tunnel.Empty, error) {
	c.pings.Add(1)
	return &tunnel.Empty{}, nil
}

func TestCredentialsRejectNonLoopbackHostBeforeDispatch(t *testing.T) {
	for _, host := range []string{
		"attacker.invalid:12049", "localhost.attacker", "127.0.0.1.attacker",
		"192.0.2.1", "[2001:db8::1]:12049", "0.0.0.0", "",
	} {
		t.Run(host, func(t *testing.T) {
			client := &hostGuardClient{}
			identity := readyFixture()
			handler := newPreparedHandler(t.Context(), client, identity.Owner, identity)
			for _, path := range []string{"/ready", ownerPath, "/github-credentials", "/git-credentials"} {
				request := httptest.NewRequest(http.MethodPost, "http://localhost"+path, nil)
				if path == "/ready" || path == ownerPath {
					request.Method = http.MethodGet
				}
				request.Host = host
				request.Header.Set(HeaderCredentialsInstance, identity.Instance)
				request.Header.Set(HeaderCredentialsHostOwner, identity.HostOwner)
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				require.Equal(t, http.StatusForbidden, response.Code, path)
			}
			require.Zero(t, client.pings.Load())
			require.Zero(t, client.calls.Load())
		})
	}
}

func TestCredentialsAcceptLoopbackHosts(t *testing.T) {
	for _, host := range []string{
		"localhost", "LOCALHOST:12049", "127.0.0.1", "127.0.0.1:12049",
		"127.0.0.2:12049", "[::1]", "[::1]:12049",
	} {
		t.Run(host, func(t *testing.T) {
			client := &hostGuardClient{}
			handler := newPreparedHandler(t.Context(), client, "guest", readyFixture())
			request := httptest.NewRequest(http.MethodGet, "http://localhost/ready", nil)
			request.Host = host
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code)
			require.EqualValues(t, 1, client.pings.Load())
			require.Zero(t, client.calls.Load())
		})
	}
}
