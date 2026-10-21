package opener

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func TestBrowserReuseWaitsForPreparedHelperIdentity(t *testing.T) {
	setupTempHome(t)
	created, err := helperCreateTime(os.Getpid())
	require.NoError(t, err)
	expected := TunnelState{PID: os.Getpid(), CreateTime: created, PreparationVersion: 1}
	require.NoError(t, WriteTunnelState("ctx", "ws", expected))
	marker, err := browserPreparation("ctx", "ws", &expected)
	require.NoError(t, err)
	defer marker.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	unpreparedCtx, stopUnprepared := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stopUnprepared()
	require.False(t, waitBrowserPreparation(unpreparedCtx, "ctx", "ws", &expected))
	require.NoError(t, marker.Mark())
	require.True(t, waitBrowserPreparation(ctx, "ctx", "ws", &expected))
	other := expected
	other.CreateTime++
	require.NoError(t, WriteTunnelState("ctx", "ws", other))
	require.False(t, waitBrowserPreparation(ctx, "ctx", "ws", &expected))
}

func TestBrowserReuseRejectsLegacyAndCanceledPreparation(t *testing.T) {
	setupTempHome(t)
	expected := TunnelState{PID: 123, CreateTime: 456}
	require.NoError(t, WriteTunnelState("ctx", "ws", expected))
	require.False(t, waitBrowserPreparation(context.Background(), "ctx", "ws", &expected))
	expected.PreparationVersion = 1
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.NoError(t, WriteTunnelState("ctx", "ws", expected))
	marker, err := browserPreparation("ctx", "ws", &expected)
	require.NoError(t, err)
	defer marker.Close()
	require.NoError(t, marker.Mark())
	require.False(t, waitBrowserPreparation(ctx, "ctx", "ws", &expected))
}

func TestBrowserReuseReleasesLockWhilePreparationIsPending(t *testing.T) {
	setupTempHome(t)
	logs := log.InitTestObserved(t, zapcore.InfoLevel)
	created, err := helperCreateTime(os.Getpid())
	require.NoError(t, err)
	existing := TunnelState{PID: os.Getpid(), CreateTime: created, PreparationVersion: 1}
	require.NoError(t, WriteTunnelState("ctx", "ws", existing))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = runDaemonBrowserTunnel(ctx, daemonTunnelArgs{
			ContextName: "ctx", WorkspaceID: "ws",
			Inv: browserIDEInvocation{OpenBrowser: true, RequirePreparation: true},
		})
	}()
	require.Eventually(t, func() bool {
		return logs.FilterMessageSnippet("browser tunnel already running").Len() == 1
	}, time.Second, time.Millisecond)
	unlocked := make(chan error, 1)
	go func() {
		unlock, err := acquireTunnelLock("ctx", "ws")
		if err == nil {
			unlock()
		}
		unlocked <- err
	}()
	select {
	case err := <-unlocked:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		cancel()
		<-done
		<-unlocked
		t.Fatal("browser preparation held the shared tunnel lock")
	}
	select {
	case <-done:
		t.Fatal("browser reuse returned before pending preparation completed")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("browser reuse did not observe cancellation")
	}
}
