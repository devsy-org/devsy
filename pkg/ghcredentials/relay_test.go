package ghcredentials

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/stretchr/testify/require"
)

func relayFixtureStatus(owner, instance string) credentials.Readiness {
	return credentials.Readiness{
		Version:       credentials.ReadinessVersion,
		Owner:         "vscode",
		HostOwner:     owner,
		Instance:      instance,
		GitHubAllowed: true,
		GitHubState:   "installed",
	}
}

func relayFixtureServer(
	t *testing.T,
	ln net.Listener,
	status credentials.Readiness,
	body string,
) *atomic.Int32 {
	t.Helper()
	calls := &atomic.Int32{}
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			require.Equal(t, http.MethodGet, r.Method)
			_ = json.NewEncoder(w).Encode(status)
			return
		}
		require.Equal(t, "/github-credentials", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, status.Instance, r.Header.Get(credentials.HeaderCredentialsInstance))
		require.Equal(t, status.HostOwner, r.Header.Get(credentials.HeaderCredentialsHostOwner))
		calls.Add(1)
		_, _ = w.Write([]byte(body))
	}))
	s.Listener = ln
	s.Start()
	t.Cleanup(s.Close)
	return calls
}

func TestRelayFetchPinsSelectedFamilyAndOwner(t *testing.T) {
	v4, err := net.Listen("tcp4", "127.0.0.1:0")
	require.NoError(t, err)
	port := v4.Addr().(*net.TCPAddr).Port
	v6, err := net.Listen("tcp6", net.JoinHostPort("::1", strconv.Itoa(port)))
	if err != nil {
		_ = v4.Close()
		t.Skipf("OS IPv6 loopback unavailable: %v", err)
	}
	first := relayFixtureServer(
		t,
		v4,
		relayFixtureStatus("host-one", "one"),
		`{"status":"available","accounts":[{"host":"github.com","user":"alice","token":"dummy-one"}]}`,
	)
	second := relayFixtureServer(
		t,
		v6,
		relayFixtureStatus("host-two", "two"),
		`{"status":"available","accounts":[{"host":"github.com","user":"bob","token":"dummy-two"}]}`,
	)
	t.Setenv("DEVSY_CREDENTIALS_SERVER_PORT", strconv.Itoa(port))
	t.Setenv("DEVSY_GH_HOST_OWNER", "host-two")
	t.Setenv("DEVSY_GH_INSTANCE", "two")
	t.Setenv("HTTP_PROXY", "http://invalid.invalid:1")
	t.Setenv("NO_PROXY", "")
	result, err := fetch(context.Background())
	require.NoError(t, err)
	require.Len(t, result.Accounts, 1)
	require.Equal(t, "bob", result.Accounts[0].User)
	require.True(t, result.Accounts[0].Token == "dummy-two", "credentials crossed service families")
	require.Zero(t, first.Load())
	require.EqualValues(t, 1, second.Load())
}

func TestRelayFetchRejectsMalformedTrailingAndOversizedResponses(t *testing.T) {
	for _, body := range []string{
		"{", `{"status":"available"} trailing`,
		`{"status":"available"}` + strings.Repeat(" ", 1<<20),
	} {
		ln, err := net.Listen("tcp4", "127.0.0.1:0")
		require.NoError(t, err)
		calls := relayFixtureServer(t, ln, relayFixtureStatus("host-one", "one"), body)
		t.Setenv("DEVSY_CREDENTIALS_SERVER_PORT", strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
		t.Setenv("DEVSY_GH_HOST_OWNER", "host-one")
		t.Setenv("DEVSY_GH_INSTANCE", "one")
		_, err = fetch(context.Background())
		require.Error(t, err)
		require.EqualValues(t, 1, calls.Load())
	}
}
