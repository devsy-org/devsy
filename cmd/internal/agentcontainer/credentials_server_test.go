package agentcontainer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/agent/tunnel"
	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestClaimPort_SucceedsWhenPortFree(t *testing.T) {
	ln, err := claimPort(0)
	require.NoError(t, err)
	_ = ln.Close()
}

func TestClaimPort_ErrorsWhenPortHeld(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	_, err = claimPort(port)
	require.Error(t, err)
	assert.ErrorIs(t, err, errPortOwnedByAnotherSession)
}

func TestClaimPort_BecomesClaimableAfterHolderReleases(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = ln.Close()
		}
	})
	port := ln.Addr().(*net.TCPAddr).Port

	_, err = claimPort(port)
	require.Error(t, err, "port must read as unavailable while the listener is held")

	require.NoError(t, ln.Close())
	closed = true

	claimed, err := claimPort(port)
	require.NoError(t, err, "port must read as claimable once the prior holder releases it")
	_ = claimed.Close()
}

func TestClaimPort_OnlyOneConcurrentCallerWins(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	const callers = 20
	var wg sync.WaitGroup
	var successes int
	var mu sync.Mutex
	var winner net.Listener

	for range callers {
		wg.Go(func() {
			claimedLn, claimErr := claimPort(port)
			if claimErr != nil {
				return
			}
			mu.Lock()
			successes++
			winner = claimedLn
			mu.Unlock()
		})
	}
	wg.Wait()

	assert.Equal(t, 1, successes, "exactly one concurrent caller must win the claim")
	if winner != nil {
		_ = winner.Close()
	}
}

func startFakeCredentialsServer(t *testing.T, owner string) int {
	t.Helper()

	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = credentials.RunCredentialsServerWithListener(ctx, ln, &fakeCredentialsClient{}, owner)
	}()

	require.Eventually(t, func() bool {
		conn, dialErr := net.Dial("tcp", ln.Addr().String())
		if dialErr != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, time.Second, 5*time.Millisecond, "fake credentials server must become dialable")

	return port
}

func TestCredentialsServerCmd_Run_SameOwnerCollisionIsSilentNoOp(t *testing.T) {
	port := startFakeCredentialsServer(t, "alice")

	var sink strings.Builder
	log.Init(log.Config{Verbosity: 2, Format: "json"})
	remove := log.AddSink(&sink)
	defer remove()

	cmd := &CredentialsServerCmd{User: "alice"}
	err := cmd.Run(context.Background(), port)
	require.NoError(t, err, "losing to the same owner's session must not be an error")
	_ = log.Sync()

	assert.NotContains(t, sink.String(), "\"level\":\"warn\"", "same-owner collision must not warn")
}

func TestCredentialsServerCmd_Run_DifferentOwnerCollisionWarnsButDoesNotError(t *testing.T) {
	port := startFakeCredentialsServer(t, "alice")

	var sink strings.Builder
	log.Init(log.Config{Verbosity: 2, Format: "json"})
	remove := log.AddSink(&sink)
	defer remove()

	cmd := &CredentialsServerCmd{User: "root"}
	err := cmd.Run(context.Background(), port)
	require.NoError(t, err, "losing the race must still not fail the session")
	_ = log.Sync()

	logged := sink.String()
	assert.Contains(t, logged, "root", "warning must name the user left without credentials")
	assert.Contains(t, logged, "alice", "warning must name the owning session")
}

type fakeCredentialsClient struct{}

func (fakeCredentialsClient) GitCredentials(
	_ context.Context, _ *tunnel.Message, _ ...grpc.CallOption,
) (*tunnel.Message, error) {
	return nil, fmt.Errorf("not implemented")
}

func (fakeCredentialsClient) DockerCredentials(
	_ context.Context, _ *tunnel.Message, _ ...grpc.CallOption,
) (*tunnel.Message, error) {
	return nil, fmt.Errorf("not implemented")
}

func (fakeCredentialsClient) GitSSHSignature(
	_ context.Context, _ *tunnel.Message, _ ...grpc.CallOption,
) (*tunnel.Message, error) {
	return nil, fmt.Errorf("not implemented")
}

func (fakeCredentialsClient) GPGPublicKeys(
	_ context.Context, _ *tunnel.Message, _ ...grpc.CallOption,
) (*tunnel.Message, error) {
	return nil, fmt.Errorf("not implemented")
}

func (fakeCredentialsClient) DevsyConfig(
	_ context.Context, _ *tunnel.Message, _ ...grpc.CallOption,
) (*tunnel.Message, error) {
	return nil, fmt.Errorf("not implemented")
}

func TestClaimPort_WrapsNonAddrInUseErrorsWithoutSentinel(t *testing.T) {
	_, err := claimPort(-1)
	require.Error(t, err)
	assert.False(t, errors.Is(err, errPortOwnedByAnotherSession))
}
