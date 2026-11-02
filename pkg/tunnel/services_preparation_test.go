package tunnel

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/credentials"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func TestServicePreparationWaitsForInstallAndServingWithoutEndingLifetime(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	install, serve := make(chan struct{}), make(chan struct{})
	probeStarted := make(chan struct{})
	installDone := make(chan struct{})
	runDone := make(chan struct{})
	runner := servicePreparation{
		run: func() error { defer close(runDone); <-ctx.Done(); return nil },
		probe: func(ctx context.Context) credentials.Readiness {
			close(probeStarted)
			if !waitPreparationSignal(ctx, install) {
				return credentials.Readiness{GitHubState: credentialsUnavailable}
			}
			close(installDone)
			if !waitPreparationSignal(ctx, serve) {
				return credentials.Readiness{GitHubState: credentialsUnavailable}
			}
			return credentials.Readiness{GitHubState: installedPreparation}
		},
	}
	ready := make(chan credentials.Readiness, 1)
	go func() { ready <- runner.start(ctx, true) }()
	require.True(t, waitPreparationSignal(ctx, probeStarted))
	assertPreparationPending(t, ready)
	close(install)
	require.True(t, waitPreparationSignal(ctx, installDone))
	assertPreparationPending(t, ready)
	close(serve)
	require.Equal(t, installedPreparation, receivePreparation(t, ctx, ready).GitHubState)
	assertPreparationPending(t, runDone)
	cancel()
	require.True(t, waitPreparationSignal(context.Background(), runDone))
}

func waitPreparationSignal(ctx context.Context, signal <-chan struct{}) bool {
	select {
	case <-signal:
		return true
	case <-ctx.Done():
		return false
	case <-time.After(time.Second):
		return false
	}
}

func assertPreparationPending[T any](t *testing.T, c <-chan T) {
	t.Helper()
	select {
	case <-c:
		t.Fatal("preparation crossed an unreleased barrier")
	default:
	}
}

func receivePreparation(
	t *testing.T,
	ctx context.Context,
	c <-chan credentials.Readiness,
) credentials.Readiness {
	t.Helper()
	select {
	case status := <-c:
		return status
	case <-ctx.Done():
		t.Fatal("preparation timed out")
		return credentials.Readiness{}
	}
}

func TestServicePreparationFailureCancellationAndDisabledHaveBoundedOutcomes(t *testing.T) {
	for _, mode := range []string{
		startupFailedPreparation, cancelledPreparation,
		disabledPreparation, missingCLIPreparation,
	} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			runner := servicePreparation{
				run: func() error {
					defer close(done)
					if mode == startupFailedPreparation {
						return errors.New("startup failed")
					}
					<-ctx.Done()
					return nil
				},
				probe: func(ctx context.Context) credentials.Readiness {
					if mode == missingCLIPreparation {
						return credentials.Readiness{GitHubState: "backend_missing"}
					}
					<-ctx.Done()
					return credentials.Readiness{GitHubState: "unavailable"}
				},
			}
			if mode == cancelledPreparation {
				cancel()
			}
			status := runner.start(ctx, mode != disabledPreparation)
			want := map[string]string{
				startupFailedPreparation: credentialsUnavailable, cancelledPreparation: credentialsUnavailable,
				disabledPreparation: disabledPreparation, missingCLIPreparation: "backend_missing",
			}[mode]
			require.Equal(t, want, status.GitHubState)
			env := GitHubSessionEnvironment(status)
			if mode != disabledPreparation {
				require.Equal(t, "unavailable", env["DEVSY_GH_FORWARDING"])
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("service goroutine did not stop")
			}
		})
	}
}

const cancelledPreparation = "cancelled"

const disabledPreparation = "disabled"

const missingCLIPreparation = "missing-cli"

const startupFailedPreparation = "startup-failed"

func TestServicePreparationReportsFailureThroughoutServiceLifetime(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		enabled, ready, cancelled bool
	}{
		{name: disabledPreparation},
		{name: "startup", enabled: true},
		{name: "after-ready", enabled: true, ready: true},
		{name: cancelledPreparation, enabled: true, cancelled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := log.InitTestObserved(t, zapcore.WarnLevel)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			fail := make(chan struct{})
			runner := servicePreparation{
				run: func() error { <-fail; return errors.New("dummy-secret-must-not-be-logged") },
				probe: func(ctx context.Context) credentials.Readiness {
					if tc.ready {
						return credentials.Readiness{GitHubState: installedPreparation}
					}
					<-ctx.Done()
					return credentials.Readiness{GitHubState: credentialsUnavailable}
				},
			}
			if tc.cancelled {
				cancel()
			}
			if !tc.ready {
				close(fail)
			}
			status := runner.start(ctx, tc.enabled)
			if tc.ready {
				require.Equal(t, installedPreparation, status.GitHubState)
				close(fail)
			}
			if tc.cancelled {
				require.Empty(t, logs.All())
				return
			}
			require.Eventually(
				t,
				func() bool { return logs.FilterMessage("credential services stopped unexpectedly").Len() == 1 },
				time.Second,
				time.Millisecond,
			)
			require.NotContains(t, fmt.Sprint(logs.All()), "dummy-secret")
		})
	}
}

const installedPreparation = "installed"
