package task

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/stretchr/testify/require"
)

func TestLegacyPendingLaunchDoesNotTrustPreviousWorkerExit(t *testing.T) {
	for _, scenario := range []struct {
		name            string
		cancelRequested bool
		terminal        bool
	}{
		{name: "pending"},
		{name: "cancel requested", cancelRequested: true},
		{name: "already failed", terminal: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store, tk, unpublished := ambiguousLegacyLaunch(t)
			require.NoError(t, store.update(tk.ID(), func(s *State) {
				s.CancelRequested = scenario.cancelRequested
				if scenario.terminal {
					s.Status = StatusFailed
					s.Error = "original worker failure"
					s.ErrorCode = "original_code"
				}
			}))
			before, err := store.Get(tk.ID())
			require.NoError(t, err)
			terminateCalls, cleanupCalls := 0, 0
			store.processController = fakeProcessController{
				terminate: func(command.ProcessRef) error { terminateCalls++; return nil },
				cleanup:   func(command.ProcessRef) error { cleanupCalls++; return nil },
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			state, err := store.ReconcileTask(ctx, tk.ID())
			require.ErrorContains(t, err, "legacy abandoned launch")
			require.Equal(t, before.CancelRequested, state.CancelRequested)
			requireLegacyLaunchUnverified(t, before, state)
			require.ErrorContains(t, tk.CancelContext(ctx), "legacy abandoned launch")
			state, err = store.Get(tk.ID())
			require.NoError(t, err)
			require.Equal(t, !scenario.terminal || before.CancelRequested, state.CancelRequested)
			requireLegacyLaunchUnverified(t, before, state)
			require.Zero(t, terminateCalls)
			require.Zero(t, cleanupCalls)
			exited, err := command.ProcessExited(unpublished)
			require.NoError(t, err)
			require.False(t, exited, "the unpublished attempt must remain alive")
		})
	}
}

func ambiguousLegacyLaunch(t *testing.T) (*Store, *Task, command.ProcessRef) {
	t.Helper()
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{})
	require.NoError(t, err)
	previous := legacyLaunchChild(t)
	require.NoError(t, tk.SetPID(previous.Process.Pid))
	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	previousRef, ok := state.ProcessReference()
	require.True(t, ok)
	require.NotEmpty(t, previousRef.Identity)
	require.NoError(t, previous.Process.Kill())
	require.Error(t, previous.Wait())
	exited, err := command.ProcessExited(previousRef)
	require.NoError(t, err)
	require.True(t, exited)
	unpublished := legacyLaunchChild(t)
	identity, err := command.ProcessTreeIdentity(unpublished.Process.Pid)
	require.NoError(t, err)
	require.NotEmpty(t, identity)
	// Legacy BeginLaunch could retain an earlier attempt's reference; the current
	// protocol rejects reuse, so this fixture restores that ambiguous stored state.
	require.NoError(t, store.update(tk.ID(), func(s *State) { s.LaunchPending = true }))
	return store, tk, command.ProcessRef{PID: unpublished.Process.Pid, Identity: identity}
}

func legacyLaunchChild(t *testing.T) *exec.Cmd {
	t.Helper()
	child := startupTestChild()
	require.NoError(t, child.Start())
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	return child
}

func requireLegacyLaunchUnverified(t *testing.T, before, after *State) {
	t.Helper()
	require.True(t, after.LaunchPending)
	require.False(t, after.ProcessCleanupComplete)
	require.Equal(t, before.Status, after.Status)
	require.Equal(t, before.Error, after.Error)
	require.Equal(t, before.ErrorCode, after.ErrorCode)
}
