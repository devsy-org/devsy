package task

import (
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func useTempHome(t *testing.T) {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
}

func recordedRun(t *testing.T, failures int) string {
	t.Helper()
	store, err := workspacelogs.OpenDefault()
	require.NoError(t, err)
	m, err := store.CreateRun(workspacelogs.RunSpec{Context: "default", Command: "up"})
	require.NoError(t, err)
	rec, err := workspacelogs.NewRecorder(workspacelogs.RecorderOptions{
		Backend: store, RunID: m.RunID, Location: workspacelogs.LocationContainer,
	})
	require.NoError(t, err)
	for range failures {
		rec.Invocation(workspacelogs.Invocation{
			ID:    workspacelogs.NewID(),
			Hook:  "postCreateCommand",
			State: workspacelogs.InvocationFailed,
		})
	}
	rec.Close(workspacelogs.ProducerTerminal, true)

	return m.RunID
}

func TestFailedTaskCarriesRunAndUniqueFailedInvocation(t *testing.T) {
	useTempHome(t)
	runID := recordedRun(t, 1)
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{Command: "up", WorkspaceID: "ws", RunID: runID})
	require.NoError(t, err)

	require.NoError(t, tk.Fail(errors.New("hook failed")))

	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	assert.Equal(t, runID, state.RunID)
	assert.Equal(t, runID, state.ErrorContext[workspacelogs.ContextRunID])
	assert.NotEmpty(t, state.ErrorContext[workspacelogs.ContextFailedInvocationID])
}

func TestFailedTaskOmitsInvocationWhenSeveralFailed(t *testing.T) {
	useTempHome(t)
	runID := recordedRun(t, 2)
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{Command: "up", RunID: runID})
	require.NoError(t, err)

	require.NoError(t, tk.Fail(errors.New("boom")))

	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	assert.Equal(t, runID, state.ErrorContext[workspacelogs.ContextRunID])
	assert.NotContains(t, state.ErrorContext, workspacelogs.ContextFailedInvocationID)
}

func TestFailedTaskWithoutRunKeepsContextUnchanged(t *testing.T) {
	useTempHome(t)
	store := newTestStore(t)
	tk, err := store.Create(CreateOptions{Command: "up"})
	require.NoError(t, err)

	require.NoError(t, tk.Fail(errors.New("boom")))

	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	assert.NotContains(t, state.ErrorContext, workspacelogs.ContextRunID)
}
