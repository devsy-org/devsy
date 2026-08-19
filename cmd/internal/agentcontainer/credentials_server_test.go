package agentcontainer

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestCredentialsServerCmd_Run_TreatsPortOwnedByAnotherSessionAsNoOp(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	cmd := &CredentialsServerCmd{}
	err = cmd.Run(context.Background(), port)
	require.NoError(t, err, "losing the port claim to another session must not be an error")
}

func TestClaimPort_WrapsNonAddrInUseErrorsWithoutSentinel(t *testing.T) {
	_, err := claimPort(-1)
	require.Error(t, err)
	assert.False(t, errors.Is(err, errPortOwnedByAnotherSession))
}
