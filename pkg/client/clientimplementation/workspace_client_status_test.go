package clientimplementation

import (
	"errors"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testWorkspaceID = "my-ws"

// fakeTaskDirPathManager overrides only TaskDir so tests can point pkg/task
// at a temp directory without touching the real state dir.
type fakeTaskDirPathManager struct {
	config.PathManager
	dir string
}

func (f fakeTaskDirPathManager) TaskDir() (string, error) { return f.dir, nil }

func useTempTaskDir(t *testing.T) {
	t.Helper()
	config.SetPathManager(fakeTaskDirPathManager{
		PathManager: config.NewPathManager(),
		dir:         t.TempDir(),
	})
	t.Cleanup(config.ResetPathManager)
}

func assertOverride(t *testing.T, s *workspaceClient, wantOK bool, wantStatus client.Status) {
	t.Helper()
	got, ok := s.taskStatusOverride()
	assert.Equal(t, wantOK, ok)
	if wantOK {
		assert.Equal(t, wantStatus, got)
	}
}

func TestTaskStatusOverride_NoTasks(t *testing.T) {
	useTempTaskDir(t)
	s := &workspaceClient{workspace: &provider.Workspace{ID: testWorkspaceID}}
	assertOverride(t, s, false, "")
}

func TestTaskStatusOverride_ActiveUpTaskReportsProvisioning(t *testing.T) {
	useTempTaskDir(t)
	store, err := task.NewStore()
	require.NoError(t, err)
	_, err = store.Create(task.CreateOptions{Command: "up", WorkspaceID: testWorkspaceID})
	require.NoError(t, err)

	s := &workspaceClient{workspace: &provider.Workspace{ID: testWorkspaceID}}
	assertOverride(t, s, true, client.StatusProvisioning)
}

func TestTaskStatusOverride_FailedTaskReportsFailed(t *testing.T) {
	useTempTaskDir(t)
	store, err := task.NewStore()
	require.NoError(t, err)
	tsk, err := store.Create(task.CreateOptions{Command: "up", WorkspaceID: testWorkspaceID})
	require.NoError(t, err)
	require.NoError(t, tsk.Fail(errors.New("build failed")))

	s := &workspaceClient{workspace: &provider.Workspace{ID: testWorkspaceID}}
	assertOverride(t, s, true, client.StatusFailed)
}

func TestTaskStatusOverride_SucceededTaskDefersToContainerStatus(t *testing.T) {
	useTempTaskDir(t)
	store, err := task.NewStore()
	require.NoError(t, err)
	tsk, err := store.Create(task.CreateOptions{Command: "up", WorkspaceID: testWorkspaceID})
	require.NoError(t, err)
	require.NoError(t, tsk.Succeed(nil))

	s := &workspaceClient{workspace: &provider.Workspace{ID: testWorkspaceID}}
	assertOverride(t, s, false, "")
}

func TestTaskStatusOverride_IgnoresOtherWorkspaces(t *testing.T) {
	useTempTaskDir(t)
	store, err := task.NewStore()
	require.NoError(t, err)
	_, err = store.Create(task.CreateOptions{Command: "up", WorkspaceID: "other-ws"})
	require.NoError(t, err)

	s := &workspaceClient{workspace: &provider.Workspace{ID: testWorkspaceID}}
	assertOverride(t, s, false, "")
}

func TestTaskStatusOverride_IgnoresNonUpCommands(t *testing.T) {
	useTempTaskDir(t)
	store, err := task.NewStore()
	require.NoError(t, err)
	_, err = store.Create(task.CreateOptions{Command: "delete", WorkspaceID: testWorkspaceID})
	require.NoError(t, err)

	s := &workspaceClient{workspace: &provider.Workspace{ID: testWorkspaceID}}
	assertOverride(t, s, false, "")
}

func TestTaskStatusOverride_MostRecentTaskWins(t *testing.T) {
	useTempTaskDir(t)
	store, err := task.NewStore()
	require.NoError(t, err)

	older, err := store.Create(task.CreateOptions{Command: "up", WorkspaceID: testWorkspaceID})
	require.NoError(t, err)
	require.NoError(t, older.Fail(errors.New("first attempt failed")))

	// Real wall-clock gap so the second task is unambiguously "most recent".
	time.Sleep(5 * time.Millisecond)
	_, err = store.Create(task.CreateOptions{Command: "up", WorkspaceID: testWorkspaceID})
	require.NoError(t, err)

	s := &workspaceClient{workspace: &provider.Workspace{ID: testWorkspaceID}}
	// The second (later) task is still in flight, so it wins over the
	// earlier failure.
	assertOverride(t, s, true, client.StatusProvisioning)
}
