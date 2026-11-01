package agentcontainer

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitHubCredentialsClaimWaitsForDrainingListener(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	server := &http.Server{ReadHeaderTimeout: time.Second}
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = ln.Close()
	})
	joined := make(chan struct{})
	go func() { defer close(joined); _ = server.Serve(ln) }()
	t.Cleanup(func() { _ = server.Close(); <-joined })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := &CredentialsServerCmd{ConfigureGitHubHelper: true}
	claimed, err := cmd.claimPort(ctx, port)
	require.NoError(t, err, "a draining prior session must not suppress the next service")
	require.NoError(t, claimed.Close())
}

func TestGitHubCredentialsClaimPreservesLiveOwner(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	status := credentials.Readiness{
		Version: credentials.ReadinessVersion, Owner: "other-user", HostOwner: "other-host",
		Instance: "live-instance", GitHubAllowed: true, GitHubState: "installed",
	}
	server := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/ready", r.URL.Path)
			_ = json.NewEncoder(w).Encode(status)
		}),
	}
	joined := make(chan struct{})
	go func() { defer close(joined); _ = server.Serve(ln) }()
	t.Cleanup(func() { _ = server.Close(); <-joined })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := &CredentialsServerCmd{User: "vscode", HostOwner: "new-host", ConfigureGitHubHelper: true}
	claimed, err := cmd.claimPort(ctx, port)
	require.Nil(t, claimed)
	require.ErrorIs(t, err, errPortOwnedByAnotherSession)
	_, err = credentials.ProbeReadiness(ctx, port, status)
	require.NoError(t, err, "a live owner must remain untouched")
}

func TestGitHubCredentialsClaimCancellationPreservesUnresponsiveListener(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := &CredentialsServerCmd{ConfigureGitHubHelper: true}
	done := make(chan error, 1)
	go func() {
		claimed, claimErr := cmd.claimPort(ctx, port)
		if claimed != nil {
			_ = claimed.Close()
		}
		done <- claimErr
	}()
	require.NoError(t, ln.(*net.TCPListener).SetDeadline(time.Now().Add(3*time.Second)))
	conn, err := ln.Accept()
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	cancel()
	err = <-done
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, errPortOwnedByAnotherSession)
	_, err = claimPort(port)
	require.ErrorIs(t, err, errPortOwnedByAnotherSession)
}

func TestGitHubCredentialsClaimBoundsUnavailableListener(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := &CredentialsServerCmd{ConfigureGitHubHelper: true}
	claimed, err := cmd.claimPort(ctx, ln.Addr().(*net.TCPAddr).Port)
	require.Nil(t, claimed)
	require.ErrorIs(t, err, errPortOwnedByAnotherSession)
	require.NoError(
		t,
		ctx.Err(),
		"an unavailable listener must not consume the caller's entire deadline",
	)
}
