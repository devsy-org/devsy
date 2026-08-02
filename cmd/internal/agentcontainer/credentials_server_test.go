package agentcontainer

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckPortClaimable_SucceedsWhenPortFree(t *testing.T) {
	// checkPortClaimable itself binds the port to check it, so pass 0 to let
	// the OS assign a free ephemeral port at call time — this avoids the
	// close-then-probe race of reserving a port via net.Listen, closing it,
	// and hoping nothing else claims it before checkPortClaimable runs.
	assert.NoError(t, checkPortClaimable(0))
}

func TestCheckPortClaimable_ErrorsWhenPortHeld(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port

	err = checkPortClaimable(port)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
}

func TestCheckPortClaimable_BecomesClaimableAfterHolderReleases(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = ln.Close()
		}
	})
	port := ln.Addr().(*net.TCPAddr).Port

	require.Error(t, checkPortClaimable(port),
		"port must read as unavailable while the listener is held")

	require.NoError(t, ln.Close())
	closed = true

	assert.NoError(t, checkPortClaimable(port),
		"port must read as claimable once the prior holder releases it")
}
