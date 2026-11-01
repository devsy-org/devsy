package credentials

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func readyFixture() Readiness {
	return Readiness{
		Version:       ReadinessVersion,
		Owner:         "vscode",
		HostOwner:     "host-one",
		Instance:      "instance-one",
		GitHubAllowed: true,
		GitHubState:   "installed",
	}
}

func readinessServer(t *testing.T, network, address string, h http.Handler) *httptest.Server {
	t.Helper()
	ln, err := net.Listen(network, address)
	if err != nil && network == "tcp6" {
		t.Skipf("OS IPv6 loopback unavailable: %v", err)
	}
	require.NoError(t, err)
	s := httptest.NewUnstartedServer(h)
	s.Listener = ln
	s.Start()
	t.Cleanup(s.Close)
	return s
}

func TestReadinessLoopbackFamiliesPinIdentityWithoutProxy(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://invalid.invalid:1")
	t.Setenv("HTTPS_PROXY", "http://invalid.invalid:1")
	t.Setenv("NO_PROXY", "")
	for _, family := range []struct{ network, address string }{{"tcp4", "127.0.0.1:0"}, {"tcp6", "[::1]:0"}} {
		t.Run(family.network, func(t *testing.T) {
			s := readinessServer(
				t,
				family.network,
				family.address,
				newPreparedHandler(context.Background(), nil, "vscode", readyFixture()),
			)
			port := s.Listener.Addr().(*net.TCPAddr).Port
			got, err := ProbeReadiness(context.Background(), port, readyFixture())
			require.NoError(t, err)
			require.Equal(t, s.Listener.Addr().String(), got.Address)
			require.True(t, got.Matches(readyFixture()))
		})
	}
}

func TestReadinessRejectsOldOccupiedMismatchedAndUnboundedListeners(t *testing.T) {
	good, _ := json.Marshal(readyFixture())
	for _, body := range []string{"{}", "not-json", string(good) + " trailing", string(good) + strings.Repeat(" ", 4096)} {
		s := readinessServer(
			t,
			"tcp4",
			"127.0.0.1:0",
			http.HandlerFunc(
				func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) },
			),
		)
		_, err := ProbeReadiness(
			context.Background(),
			s.Listener.Addr().(*net.TCPAddr).Port,
			readyFixture(),
		)
		require.Error(t, err)
	}
	for _, change := range []func(*Readiness){
		func(r *Readiness) { r.Version-- },
		func(r *Readiness) { r.HostOwner = "host-two" },
		func(r *Readiness) { r.GitHubAllowed = false },
		func(r *Readiness) { r.Instance = "instance-two" },
		func(r *Readiness) { r.GitHubState = "installing" },
	} {
		other := readyFixture()
		change(&other)
		s := readinessServer(
			t,
			"tcp4",
			"127.0.0.1:0",
			newPreparedHandler(context.Background(), nil, "vscode", other),
		)
		_, err := ProbeReadiness(
			context.Background(),
			s.Listener.Addr().(*net.TCPAddr).Port,
			readyFixture(),
		)
		require.Error(t, err)
	}
}

func TestReadinessRejectsRedirectAndSharesDeadline(t *testing.T) {
	target := httptest.NewServer(
		http.HandlerFunc(
			func(http.ResponseWriter, *http.Request) { t.Error("readiness followed redirect") },
		),
	)
	defer target.Close()
	s := readinessServer(
		t,
		"tcp4",
		"127.0.0.1:0",
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) },
		),
	)
	_, err := ProbeReadiness(
		context.Background(),
		s.Listener.Addr().(*net.TCPAddr).Port,
		readyFixture(),
	)
	require.Error(t, err)
	stalled := readinessServer(
		t,
		"tcp4",
		"127.0.0.1:0",
		http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = ProbeReadiness(ctx, stalled.Listener.Addr().(*net.TCPAddr).Port, readyFixture())
	require.Error(t, err)
	require.Less(t, time.Since(started), time.Second)
}
