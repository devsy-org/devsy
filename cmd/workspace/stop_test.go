package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stopRecordingClient struct {
	*fakeWorkspaceClient
	calls *[]string
}

func (c *stopRecordingClient) Stop(ctx context.Context, opts client2.StopOptions) error {
	*c.calls = append(*c.calls, "stop")
	return c.fakeWorkspaceClient.Stop(ctx, opts)
}

func newStopTestConfig() *config.Config {
	return &config.Config{
		DefaultContext: testContext,
		Contexts:       map[string]*config.ContextConfig{testContext: {}},
	}
}

func TestStopCancelsPersistedUpTaskBeforeProviderStop(t *testing.T) {
	var calls []string
	fake := &fakeWorkspaceClient{
		workspace: testWorkspaceName,
		context:   testContext,
		provider:  testProvider,
		config:    &provider.Workspace{ID: testWorkspaceName, Context: testContext},
		status:    client2.StatusRunning,
	}
	client := &stopRecordingClient{fakeWorkspaceClient: fake, calls: &calls}
	cmd := &StopCmd{GlobalFlags: &flags.GlobalFlags{ResultFormat: formatPlain}}
	cmd.quiesceUpTasks = func(workspaceID string) error {
		assert.Equal(t, testWorkspaceName, workspaceID)
		calls = append(calls, "quiesce")
		return nil
	}

	require.NoError(t, cmd.run(t.Context(), newStopTestConfig(), client))

	require.NotEmpty(t, calls)
	assert.Equal(t, "quiesce", calls[0])
	assert.Contains(t, calls, "stop")
	stopIndex := -1
	for i, call := range calls {
		if call == "stop" {
			stopIndex = i
			break
		}
	}
	quiesceBeforeStop := 0
	for _, call := range calls[:stopIndex] {
		if call == "quiesce" {
			quiesceBeforeStop++
		}
	}
	assert.Equal(t, 2, quiesceBeforeStop, "stop must quiesce before and after the workspace lock")
}

func TestStopDoesNotProceedWhenActiveTaskCannotBeKilled(t *testing.T) {
	fake := &fakeWorkspaceClient{
		workspace: testWorkspaceName,
		context:   testContext,
		provider:  testProvider,
		config:    &provider.Workspace{ID: testWorkspaceName, Context: testContext},
		status:    client2.StatusRunning,
	}
	var calls []string
	client := &stopRecordingClient{fakeWorkspaceClient: fake, calls: &calls}
	cmd := &StopCmd{GlobalFlags: &flags.GlobalFlags{ResultFormat: formatPlain}}
	cmd.quiesceUpTasks = func(string) error {
		return errors.New("boom")
	}

	err := cmd.run(t.Context(), newStopTestConfig(), client)
	require.Error(t, err)
	assert.NotContains(t, calls, "stop")
}

func TestStopFindsTaskWithoutDesktopState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DEVSY_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)

	// A persisted up task with no live worker, as a Desktop restart leaves
	// behind.
	store, err := task.NewStore()
	require.NoError(t, err)
	tk, err := store.Create(task.CreateOptions{Command: "up", WorkspaceID: testWorkspaceName})
	require.NoError(t, err)

	fake := &fakeWorkspaceClient{
		workspace: testWorkspaceName,
		context:   testContext,
		provider:  testProvider,
		config:    &provider.Workspace{ID: testWorkspaceName, Context: testContext},
		status:    client2.StatusRunning,
	}
	var calls []string
	client := &stopRecordingClient{fakeWorkspaceClient: fake, calls: &calls}
	cmd := &StopCmd{GlobalFlags: &flags.GlobalFlags{ResultFormat: formatPlain}}

	require.NoError(t, cmd.run(t.Context(), newStopTestConfig(), client))
	assert.Contains(t, calls, "stop")

	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	assert.Equal(t, task.StatusFailed, state.Status)
	assert.Equal(t, task.ErrCanceled.Error(), state.Error)
}
