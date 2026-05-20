package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	devcontainerconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testDefaultContext = "default"
)

func setupTestPathManager(t *testing.T) {
	t.Helper()

	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
}

func writeWorkspaceResult(
	t *testing.T, workspaceID string, result *devcontainerconfig.Result,
) {
	t.Helper()

	ws := &provider.Workspace{ID: workspaceID, Context: testDefaultContext}
	require.NoError(t, provider.SaveWorkspaceResult(ws, result))
}

func loadWorkspaceResult(
	t *testing.T, workspaceID string,
) *devcontainerconfig.Result {
	t.Helper()

	result, err := provider.LoadWorkspaceResult(testDefaultContext, workspaceID)
	require.NoError(t, err)
	require.NotNil(t, result)

	return result
}

func ptrStr(s string) *string { return &s }

func TestUpdateWorkspaceResult_BasicRename(t *testing.T) {
	setupTestPathManager(t)

	oldName := "my-project"
	newName := "my-project-renamed"

	result := &devcontainerconfig.Result{
		SubstitutionContext: &devcontainerconfig.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/my-project",
			LocalWorkspaceFolder:     "/home/user/my-project",
			WorkspaceMount:           "type=bind,source=/home/user/my-project,target=/workspaces/my-project",
		},
		MergedConfig: &devcontainerconfig.MergedDevContainerConfig{},
	}
	result.MergedConfig.WorkspaceFolder = "/workspaces/my-project"
	result.MergedConfig.WorkspaceMount = ptrStr(
		"type=bind,source=/home/user/my-project,target=/workspaces/my-project",
	)

	writeWorkspaceResult(t, newName, result)

	devsyConfig := &config.Config{DefaultContext: testDefaultContext}
	updateWorkspaceResult(devsyConfig, oldName, newName)

	got := loadWorkspaceResult(t, newName)

	// Container paths should NOT change (they are in-container paths)
	assert.Equal(t, "/workspaces/my-project", got.SubstitutionContext.ContainerWorkspaceFolder)
	assert.Equal(t, "/workspaces/my-project", got.MergedConfig.WorkspaceFolder)
	// Host-side paths should be updated
	assert.Equal(t, "/home/user/my-project-renamed", got.SubstitutionContext.LocalWorkspaceFolder)
	assert.Contains(t, got.SubstitutionContext.WorkspaceMount, "/home/user/my-project-renamed")
	require.NotNil(t, got.MergedConfig.WorkspaceMount)
	assert.Contains(t, *got.MergedConfig.WorkspaceMount, "/home/user/my-project-renamed")
}

func TestUpdateWorkspaceResult_MergedConfigUpdated(t *testing.T) {
	setupTestPathManager(t)

	oldName := "app"
	newName := "app-v2"

	result := &devcontainerconfig.Result{
		SubstitutionContext: &devcontainerconfig.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/app",
			LocalWorkspaceFolder:     "/home/dev/app",
			WorkspaceMount:           "type=bind,source=/home/dev/app,target=/workspaces/app",
		},
		MergedConfig: &devcontainerconfig.MergedDevContainerConfig{},
	}
	result.MergedConfig.WorkspaceFolder = "/workspaces/app"
	result.MergedConfig.WorkspaceMount = ptrStr(
		"type=bind,source=/home/dev/app,target=/workspaces/app",
	)

	writeWorkspaceResult(t, newName, result)

	devsyConfig := &config.Config{DefaultContext: testDefaultContext}
	updateWorkspaceResult(devsyConfig, oldName, newName)

	got := loadWorkspaceResult(t, newName)

	// Container path unchanged
	assert.Equal(t, "/workspaces/app", got.MergedConfig.WorkspaceFolder)
	// Host source path updated in mount
	require.NotNil(t, got.MergedConfig.WorkspaceMount)
	assert.Equal(t,
		"type=bind,source=/home/dev/app-v2,target=/workspaces/app",
		*got.MergedConfig.WorkspaceMount,
	)
}

func TestUpdateWorkspaceResult_NonDefaultWorkspaceDir(t *testing.T) {
	setupTestPathManager(t)

	oldName := "project"
	newName := "project-new"

	result := &devcontainerconfig.Result{
		SubstitutionContext: &devcontainerconfig.SubstitutionContext{
			ContainerWorkspaceFolder: "/home/coder/project",
			LocalWorkspaceFolder:     "/mnt/data/project",
			WorkspaceMount:           "type=bind,source=/mnt/data/project,target=/home/coder/project",
		},
		MergedConfig: &devcontainerconfig.MergedDevContainerConfig{},
	}
	result.MergedConfig.WorkspaceFolder = "/home/coder/project"
	result.MergedConfig.WorkspaceMount = ptrStr(
		"type=bind,source=/mnt/data/project,target=/home/coder/project",
	)

	writeWorkspaceResult(t, newName, result)

	devsyConfig := &config.Config{DefaultContext: testDefaultContext}
	updateWorkspaceResult(devsyConfig, oldName, newName)

	got := loadWorkspaceResult(t, newName)

	// Container path unchanged
	assert.Equal(t, "/home/coder/project", got.SubstitutionContext.ContainerWorkspaceFolder)
	assert.Equal(t, "/home/coder/project", got.MergedConfig.WorkspaceFolder)
	// Host-side path updated
	assert.Equal(t, "/mnt/data/project-new", got.SubstitutionContext.LocalWorkspaceFolder)
	assert.Equal(t,
		"type=bind,source=/mnt/data/project-new,target=/home/coder/project",
		got.SubstitutionContext.WorkspaceMount,
	)
	require.NotNil(t, got.MergedConfig.WorkspaceMount)
	assert.Equal(t,
		"type=bind,source=/mnt/data/project-new,target=/home/coder/project",
		*got.MergedConfig.WorkspaceMount,
	)
}

func TestUpdateWorkspaceResult_NestedPath(t *testing.T) {
	setupTestPathManager(t)

	oldName := "repo"
	newName := "repo-renamed"

	result := &devcontainerconfig.Result{
		SubstitutionContext: &devcontainerconfig.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/org/repo",
			LocalWorkspaceFolder:     "/home/user/dev/org/repo",
			WorkspaceMount:           "type=bind,source=/home/user/dev/org/repo,target=/workspaces/org/repo",
		},
		MergedConfig: &devcontainerconfig.MergedDevContainerConfig{},
	}
	result.MergedConfig.WorkspaceFolder = "/workspaces/org/repo"
	result.MergedConfig.WorkspaceMount = ptrStr(
		"type=bind,source=/home/user/dev/org/repo,target=/workspaces/org/repo",
	)

	writeWorkspaceResult(t, newName, result)

	devsyConfig := &config.Config{DefaultContext: testDefaultContext}
	updateWorkspaceResult(devsyConfig, oldName, newName)

	got := loadWorkspaceResult(t, newName)

	// Container path unchanged
	assert.Equal(t, "/workspaces/org/repo", got.SubstitutionContext.ContainerWorkspaceFolder)
	assert.Equal(t, "/workspaces/org/repo", got.MergedConfig.WorkspaceFolder)
	// Host-side path updated
	assert.Equal(t, "/home/user/dev/org/repo-renamed", got.SubstitutionContext.LocalWorkspaceFolder)
	assert.Contains(t, got.SubstitutionContext.WorkspaceMount, "/home/user/dev/org/repo-renamed")
}

func TestUpdateWorkspaceResult_SameNameIdempotent(t *testing.T) {
	setupTestPathManager(t)

	name := "my-ws"

	result := &devcontainerconfig.Result{
		SubstitutionContext: &devcontainerconfig.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/my-ws",
			LocalWorkspaceFolder:     "/home/user/my-ws",
			WorkspaceMount:           "type=bind,source=/home/user/my-ws,target=/workspaces/my-ws",
		},
		MergedConfig: &devcontainerconfig.MergedDevContainerConfig{},
	}
	result.MergedConfig.WorkspaceFolder = "/workspaces/my-ws"
	result.MergedConfig.WorkspaceMount = ptrStr(
		"type=bind,source=/home/user/my-ws,target=/workspaces/my-ws",
	)

	writeWorkspaceResult(t, name, result)

	devsyConfig := &config.Config{DefaultContext: testDefaultContext}
	updateWorkspaceResult(devsyConfig, name, name)

	got := loadWorkspaceResult(t, name)
	assert.Equal(t, "/workspaces/my-ws", got.SubstitutionContext.ContainerWorkspaceFolder)
	assert.Equal(t, "/home/user/my-ws", got.SubstitutionContext.LocalWorkspaceFolder)
	assert.Equal(t,
		"type=bind,source=/home/user/my-ws,target=/workspaces/my-ws",
		got.SubstitutionContext.WorkspaceMount,
	)
	assert.Equal(t, "/workspaces/my-ws", got.MergedConfig.WorkspaceFolder)
}

func TestUpdateWorkspaceResult_NilMergedConfig(t *testing.T) {
	setupTestPathManager(t)

	oldName := "ws-old"
	newName := "ws-new"

	result := &devcontainerconfig.Result{
		SubstitutionContext: &devcontainerconfig.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/ws-old",
			LocalWorkspaceFolder:     "/home/user/ws-old",
			WorkspaceMount:           "type=bind,source=/home/user/ws-old,target=/workspaces/ws-old",
		},
		MergedConfig: nil,
	}

	writeWorkspaceResult(t, newName, result)

	devsyConfig := &config.Config{DefaultContext: testDefaultContext}
	updateWorkspaceResult(devsyConfig, oldName, newName)

	got := loadWorkspaceResult(t, newName)

	// Container path unchanged
	assert.Equal(t, "/workspaces/ws-old", got.SubstitutionContext.ContainerWorkspaceFolder)
	// Host-side path updated
	assert.Equal(t, "/home/user/ws-new", got.SubstitutionContext.LocalWorkspaceFolder)
	assert.Contains(t, got.SubstitutionContext.WorkspaceMount, "/home/user/ws-new")
}

func TestUpdateWorkspaceResult_NilWorkspaceMount(t *testing.T) {
	setupTestPathManager(t)

	oldName := "ws-old"
	newName := "ws-new"

	result := &devcontainerconfig.Result{
		SubstitutionContext: &devcontainerconfig.SubstitutionContext{
			ContainerWorkspaceFolder: "/workspaces/ws-old",
			LocalWorkspaceFolder:     "/home/user/ws-old",
			WorkspaceMount:           "type=bind,source=/home/user/ws-old,target=/workspaces/ws-old",
		},
		MergedConfig: &devcontainerconfig.MergedDevContainerConfig{},
	}
	result.MergedConfig.WorkspaceFolder = "/workspaces/ws-old"
	result.MergedConfig.WorkspaceMount = nil

	writeWorkspaceResult(t, newName, result)

	devsyConfig := &config.Config{DefaultContext: testDefaultContext}
	updateWorkspaceResult(devsyConfig, oldName, newName)

	got := loadWorkspaceResult(t, newName)

	// Container path unchanged
	assert.Equal(t, "/workspaces/ws-old", got.MergedConfig.WorkspaceFolder)
	assert.Nil(t, got.MergedConfig.WorkspaceMount)
}

func TestUpdateWorkspaceResult_NoResultFile(t *testing.T) {
	setupTestPathManager(t)

	oldName := "nonexistent-old"
	newName := "nonexistent-new"

	wsDir, err := provider.GetWorkspaceDir(testDefaultContext, newName)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(wsDir, 0o750))

	devsyConfig := &config.Config{DefaultContext: testDefaultContext}
	updateWorkspaceResult(devsyConfig, oldName, newName)

	_, err = os.Stat(filepath.Join(wsDir, "workspace_result.json"))
	assert.True(t, os.IsNotExist(err), "should not create file when none exists")
}

func TestUpdateWorkspaceResult_PreservesOtherFields(t *testing.T) {
	setupTestPathManager(t)

	oldName := "myapp"
	newName := "myapp-v2"

	result := &devcontainerconfig.Result{
		SubstitutionContext: &devcontainerconfig.SubstitutionContext{
			DevContainerID:           "abc123",
			ContainerWorkspaceFolder: "/workspaces/myapp",
			LocalWorkspaceFolder:     "/home/user/myapp",
			WorkspaceMount:           "type=bind,source=/home/user/myapp,target=/workspaces/myapp",
			Env:                      map[string]string{"FOO": "bar"},
		},
		MergedConfig: &devcontainerconfig.MergedDevContainerConfig{},
		HostWarnings: []string{"some warning"},
	}
	result.MergedConfig.WorkspaceFolder = "/workspaces/myapp"

	writeWorkspaceResult(t, newName, result)

	devsyConfig := &config.Config{DefaultContext: testDefaultContext}
	updateWorkspaceResult(devsyConfig, oldName, newName)

	got := loadWorkspaceResult(t, newName)

	assert.Equal(t, "abc123", got.SubstitutionContext.DevContainerID)
	assert.Equal(t, map[string]string{"FOO": "bar"}, got.SubstitutionContext.Env)
	assert.Equal(t, []string{"some warning"}, got.HostWarnings)
}

func TestUpdateWorkspaceResult_RawJSON(t *testing.T) {
	setupTestPathManager(t)

	oldName := "old-ws"
	newName := "new-ws"

	wsDir, err := provider.GetWorkspaceDir(testDefaultContext, newName)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(wsDir, 0o750))

	rawJSON := `{
  "SubstitutionContext": {
    "ContainerWorkspaceFolder": "/workspaces/old-ws",
    "LocalWorkspaceFolder": "/home/user/old-ws",
    "WorkspaceMount": "type=bind,source=/home/user/old-ws,target=/workspaces/old-ws"
  },
  "MergedConfig": {
    "workspaceFolder": "/workspaces/old-ws",
    "workspaceMount": "type=bind,source=/home/user/old-ws,target=/workspaces/old-ws"
  }
}`

	resultFile := filepath.Join(wsDir, "workspace_result.json")
	require.NoError(t, os.WriteFile(resultFile, []byte(rawJSON), 0o600))

	devsyConfig := &config.Config{DefaultContext: testDefaultContext}
	updateWorkspaceResult(devsyConfig, oldName, newName)

	updatedBytes, err := os.ReadFile(resultFile) //nolint:gosec
	require.NoError(t, err)

	var got devcontainerconfig.Result
	require.NoError(t, json.Unmarshal(updatedBytes, &got))

	// Container path unchanged
	assert.Equal(t, "/workspaces/old-ws", got.SubstitutionContext.ContainerWorkspaceFolder)
	assert.Equal(t, "/workspaces/old-ws", got.MergedConfig.WorkspaceFolder)
	// Host-side path updated
	assert.Equal(t, "/home/user/new-ws", got.SubstitutionContext.LocalWorkspaceFolder)
	assert.Contains(t, got.SubstitutionContext.WorkspaceMount, "/home/user/new-ws")
	require.NotNil(t, got.MergedConfig.WorkspaceMount)
	assert.Contains(t, *got.MergedConfig.WorkspaceMount, "/home/user/new-ws")
}
