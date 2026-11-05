//go:build darwin || linux

package opener

import (
	"net"
	"os"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/tunnel"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestSpawnTunnelHelperClosesListenersWhenDevNullOpenFails(t *testing.T) {
	setupTempHome(t)
	addr := reserveLoopbackAddr(t)
	failDevNullOpen(t)

	pid, _, spawnErr := spawnTunnelHelper(spawnHelperOpts{
		ContextName: "fd-test",
		WorkspaceID: "fd-test",
		TunnelParams: tunnel.BrowserTunnelParams{
			ExtraPorts: []string{addr + ":80"},
		},
	})

	if spawnErr == nil {
		killTunnelHelper(pid)
	}
	require.ErrorIs(t, spawnErr, unix.EMFILE)
	require.True(
		t,
		strings.HasPrefix(spawnErr.Error(), "open "+os.DevNull+":"),
		"unexpected failure: %v",
		spawnErr,
	)
	listener, err := net.Listen("tcp", addr)
	require.NoError(t, err, "rebind %s after spawn failure", addr)
	require.NoError(t, listener.Close())
}

func reserveLoopbackAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())
	return addr
}

func failDevNullOpen(t *testing.T) {
	t.Helper()
	original := openDevNull
	t.Cleanup(func() { openDevNull = original })
	openDevNull = func() (*os.File, error) {
		return nil, &os.PathError{Op: "open", Path: os.DevNull, Err: unix.EMFILE}
	}
}

func killTunnelHelper(pid int) {
	if process, err := os.FindProcess(pid); err == nil {
		_ = process.Kill()
		_, _ = process.Wait()
	}
}
