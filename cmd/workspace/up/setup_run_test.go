package up

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

func TestProvisionalRunIsUnboundAndLinkedToTask(t *testing.T) {
	useTempHome(t)
	cfg := &config.Config{DefaultContext: "work"}

	runID := provisionalRun(cfg, "my-ws")
	require.NotEmpty(t, runID)
	linkRunToTask(runID, "task-1", nil)

	store, err := workspacelogs.OpenDefault()
	require.NoError(t, err)
	m, err := store.Manifest(runID)
	require.NoError(t, err)
	assert.Nil(t, m.WorkspaceUID, "the UID is bound by the worker after resolution")
	assert.Equal(t, "work", m.Context)
	assert.Equal(t, "my-ws", m.WorkspaceIDAtStart)
	assert.Equal(t, "task-1", m.TaskID)
}

func TestLinkRunToTaskMarksLaunchFailure(t *testing.T) {
	useTempHome(t)
	runID := provisionalRun(&config.Config{DefaultContext: "work"}, "ws")
	require.NotEmpty(t, runID)

	linkRunToTask(runID, "", errors.New("could not start"))
	linkRunToTask("", "t", nil)

	store, err := workspacelogs.OpenDefault()
	require.NoError(t, err)
	m, err := store.Manifest(runID)
	require.NoError(t, err)
	assert.Equal(t, workspacelogs.CommandFailed, m.CommandState)
}
