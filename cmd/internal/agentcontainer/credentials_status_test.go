package agentcontainer

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/stretchr/testify/require"
)

func TestCredentialsStatusWaitsThroughPriorSessionIdentity(t *testing.T) {
	current := credentials.Readiness{
		Version: credentials.ReadinessVersion, Owner: "vscode", HostOwner: "current-host",
		Instance: "current-instance", GitHubAllowed: true, GitHubState: "installed",
	}
	prior := current
	prior.HostOwner, prior.Instance = "prior-host", "prior-instance"
	prior.GitHubAllowed, prior.GitHubState = false, "disabled"
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		status := current
		if requests.Add(1) == 1 {
			status = prior
		}
		_ = json.NewEncoder(w).Encode(status)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	status := waitCredentialsStatus(ctx, server.Listener.Addr().(*net.TCPAddr).Port, current)
	require.True(
		t,
		status.Matches(current),
		"a prior listener must not terminate preparation before the new session is ready",
	)
	require.GreaterOrEqual(t, requests.Load(), int32(2))
}
