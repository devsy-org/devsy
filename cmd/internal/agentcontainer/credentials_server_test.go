package agentcontainer

import (
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaimPort_SucceedsWhenPortFree(t *testing.T) {
	// Pass 0 to let the OS assign a free ephemeral port at call time — this
	// avoids the close-then-probe race of reserving a port via net.Listen,
	// closing it, and hoping nothing else claims it before this call runs.
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
	assert.Contains(t, err.Error(), "not available")
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

// TestClaimPort_OnlyOneConcurrentCallerWins proves claimPort binds the port
// as the claim itself, rather than merely probing it: with a check-then-bind
// gap, two concurrent callers could both observe the port as free before
// either binds it.
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
