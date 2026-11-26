package tunnel

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/devsy-org/devsy/pkg/transport"
	"github.com/stretchr/testify/require"
)

func TestBrowserAutoOpenWaitsForInstallationAndServing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	install, serving := make(chan struct{}), make(chan struct{})
	probeStarted, installed := make(chan struct{}), make(chan struct{})
	opened, exited := make(chan struct{}), make(chan error, 1)
	serviceStopped := make(chan struct{})
	runner := servicePreparation{
		run: func() error { defer close(serviceStopped); <-ctx.Done(); return nil },
		probe: func(ctx context.Context) credentials.Readiness {
			close(probeStarted)
			if !waitPreparationSignal(ctx, install) {
				return credentials.Readiness{GitHubState: credentialsUnavailable}
			}
			close(installed)
			if !waitPreparationSignal(ctx, serving) {
				return credentials.Readiness{GitHubState: credentialsUnavailable}
			}
			return credentials.Readiness{GitHubState: installedPreparation}
		},
	}
	go func() {
		exited <- runPreparedBrowserServices(ctx, BrowserTunnelParams{
			OnPrepared: func(context.Context) { close(opened) },
		}, func(prepared func(credentials.Readiness)) error { return runner.runPrepared(ctx, true, prepared) })
	}()
	require.True(t, waitPreparationSignal(ctx, probeStarted))
	assertPreparationPending(t, opened)
	close(install)
	require.True(t, waitPreparationSignal(ctx, installed))
	assertPreparationPending(t, opened)
	close(serving)
	require.True(t, waitPreparationSignal(ctx, opened))
	assertPreparationPending(t, exited)
	cancel()
	require.True(t, waitPreparationSignal(context.Background(), serviceStopped))
	select {
	case err := <-exited:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("browser service lifetime did not stop")
	}
}

func TestDaemonBrowserAutoOpenReceivesPreparationAndKeepsLifetime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started, ready, opened := make(chan struct{}), make(chan struct{}), make(chan struct{})
	exited := make(chan error, 1)
	go func() {
		exited <- StartBrowserTunnel(ctx, BrowserTunnelParams{
			DaemonStartFunc: func(ctx context.Context, prepared func()) error {
				close(started)
				if !waitPreparationSignal(ctx, ready) {
					return ctx.Err()
				}
				prepared()
				<-ctx.Done()
				return nil
			},
			OnPrepared: func(context.Context) { close(opened) },
		})
	}()
	require.True(t, waitPreparationSignal(ctx, started))
	assertPreparationPending(t, opened)
	close(ready)
	require.True(t, waitPreparationSignal(ctx, opened))
	assertPreparationPending(t, exited)
	cancel()
	select {
	case err := <-exited:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("daemon browser lifetime did not stop")
	}
}

func TestBrowserAutoOpenDoesNotRunAfterFailureOrCancellation(t *testing.T) {
	opened := false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := BrowserTunnelParams{OnPrepared: func(context.Context) { opened = true }}
	require.NoError(
		t,
		runPreparedBrowserServices(ctx, p, func(prepared func(credentials.Readiness)) error {
			prepared(credentials.Readiness{GitHubState: credentialsUnavailable})
			return nil
		}),
	)
	require.False(t, opened)
	want := errors.New("daemon transport unavailable")
	p.DaemonStartFunc = func(context.Context, func()) error { return want }
	require.ErrorIs(t, StartBrowserTunnel(context.Background(), p), want)
	require.False(t, opened)
}

func TestBrowserServiceCompletionEndsHealthyTunnel(t *testing.T) {
	for _, tc := range []struct {
		name             string
		enabled, startup bool
	}{
		{name: "startup failure", enabled: true, startup: true},
		{name: "failure after readiness", enabled: true},
		{name: "failure with forwarding disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			finish, opened := make(chan struct{}), make(chan struct{}, 1)
			runner := browserCompletionRunner(ctx, finish, tc.startup)
			if tc.startup {
				close(finish)
			}
			exited := make(chan error, 1)
			go func() {
				exited <- runPreparedBrowserServices(ctx, BrowserTunnelParams{
					OnPrepared: func(context.Context) { opened <- struct{}{} },
				}, func(prepared func(credentials.Readiness)) error { return runner.runPrepared(ctx, tc.enabled, prepared) })
			}()
			if !tc.startup {
				require.True(t, waitPreparationSignal(ctx, opened))
				assertPreparationPending(t, exited)
				close(finish)
			}
			select {
			case err := <-exited:
				require.Error(t, err)
				require.NotContains(t, err.Error(), "dummy-secret")
			case <-time.After(time.Second):
				t.Fatal("completed services left the browser tunnel alive")
			}
			require.NoError(t, ctx.Err())
			if tc.startup {
				assertPreparationPending(t, opened)
			}
		})
	}
}

func TestBrowserPreservesCleanServiceReuseLifetime(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	completed, opened := make(chan struct{}), make(chan struct{})
	runner := servicePreparation{
		run: func() error { close(completed); return nil },
		probe: func(context.Context) credentials.Readiness {
			return credentials.Readiness{GitHubState: installedPreparation}
		},
	}
	exited := make(chan error, 1)
	go func() {
		exited <- runPreparedBrowserServices(ctx, BrowserTunnelParams{
			OnPrepared: func(context.Context) { close(opened) },
		}, func(prepared func(credentials.Readiness)) error { return runner.runPrepared(ctx, true, prepared) })
	}()
	require.True(t, waitPreparationSignal(ctx, completed))
	require.True(t, waitPreparationSignal(ctx, opened))
	select {
	case <-exited:
		t.Fatal("benign service reuse ended browser port forwarding")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-exited:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("reused browser service lifetime did not stop")
	}
}

func browserCompletionRunner(
	ctx context.Context,
	finish <-chan struct{},
	startup bool,
) servicePreparation {
	return servicePreparation{
		run: func() error {
			select {
			case <-finish:
				return errors.New("dummy-secret-service-output")
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		probe: func(ctx context.Context) credentials.Readiness {
			if startup {
				<-ctx.Done()
				return credentials.Readiness{GitHubState: credentialsUnavailable}
			}
			return credentials.Readiness{GitHubState: installedPreparation}
		},
	}
}

func TestBrowserServiceFailureClosesManagedTransport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	transportStopped := make(chan struct{})
	conn, err := transport.OpenCallbackConn(
		ctx,
		func(ctx context.Context, _ io.Reader, _ io.Writer) error {
			defer close(transportStopped)
			<-ctx.Done()
			return ctx.Err()
		},
		transport.CallbackConnOptions{},
	)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	opened, fail := make(chan struct{}), make(chan struct{})
	exited := make(chan error, 1)
	go func() {
		exited <- transport.RunManaged(transport.RunManagedOptions{
			Parent:        ctx,
			Conn:          conn,
			TransportSide: transport.SideProvider,
			Handler: func(ctx context.Context) error {
				runner := servicePreparation{
					run: func() error {
						select {
						case <-fail:
							return errors.New("dummy-secret-service-output")
						case <-ctx.Done():
							return ctx.Err()
						}
					},
					probe: func(context.Context) credentials.Readiness {
						return credentials.Readiness{GitHubState: installedPreparation}
					},
				}
				return runPreparedBrowserServices(ctx, BrowserTunnelParams{
					OnPrepared: func(context.Context) { close(opened) },
				}, func(prepared func(credentials.Readiness)) error { return runner.runPrepared(ctx, true, prepared) })
			},
		})
	}()
	require.True(t, waitPreparationSignal(ctx, opened))
	close(fail)
	select {
	case err := <-exited:
		require.EqualError(t, err, "credential services stopped unexpectedly")
	case <-ctx.Done():
		t.Fatal("failed services did not tear down the managed connection")
	}
	require.NoError(t, ctx.Err())
	require.True(t, waitPreparationSignal(ctx, transportStopped))
}
