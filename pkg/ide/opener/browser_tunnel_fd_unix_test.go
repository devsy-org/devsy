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
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	lowerNoFileSoftLimit(t)
	pressure := exhaustTunnelFDs(t)
	pid, _, spawnErr := spawnTunnelHelper(spawnHelperOpts{
		ContextName: "fd-test",
		WorkspaceID: "fd-test",
		TunnelParams: tunnel.BrowserTunnelParams{
			ExtraPorts: []string{addr + ":80"},
		},
	})
	releaseTunnelFDs(pressure)
	assertTunnelFDResult(t, addr, pid, spawnErr)
}

func lowerNoFileSoftLimit(t *testing.T) {
	t.Helper()
	var original unix.Rlimit
	require.NoError(t, unix.Getrlimit(unix.RLIMIT_NOFILE, &original))
	t.Cleanup(func() {
		require.NoError(t, unix.Setrlimit(unix.RLIMIT_NOFILE, &original))
	})
	lowered := original
	lowered.Cur = min(original.Cur, 128)
	require.NoError(t, unix.Setrlimit(unix.RLIMIT_NOFILE, &lowered))
}

func exhaustTunnelFDs(t *testing.T) []*os.File {
	t.Helper()
	var pressure []*os.File
	t.Cleanup(func() { releaseTunnelFDs(pressure) })
	for {
		f, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
		if err != nil {
			require.ErrorIs(t, err, unix.EMFILE, "exhaust descriptors")
			break
		}
		pressure = append(pressure, f)
	}
	require.GreaterOrEqual(t, len(pressure), 2, "not enough descriptors to prepare a listener")
	for range 2 {
		last := len(pressure) - 1
		require.NoError(t, pressure[last].Close())
		pressure = pressure[:last]
	}
	return pressure
}

func releaseTunnelFDs(pressure []*os.File) {
	for _, f := range pressure {
		_ = f.Close()
	}
}

func assertTunnelFDResult(t *testing.T, addr string, pid int, spawnErr error) {
	t.Helper()
	if spawnErr == nil {
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
			_, _ = process.Wait()
		}
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
