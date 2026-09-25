package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/stretchr/testify/require"
)

func TestSweepOrphanWorkspaceDirs(t *testing.T) {
	setupTestPathManager(t)

	require.NoError(t, provider.SaveWorkspaceConfig(
		&provider.Workspace{ID: "healthy", Context: testDefaultContext},
	))

	workspacesDir, err := provider.GetWorkspacesDir(testDefaultContext)
	require.NoError(t, err)

	// Orphan dir with only auxiliary state, plus a dotfile that must survive.
	orphanDir := filepath.Join(workspacesDir, "orphan")
	require.NoError(t, os.MkdirAll(filepath.Join(orphanDir, "logs"), 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(orphanDir, provider.WorkspaceResultFile), []byte("{}"), 0o600,
	))
	dotDir := filepath.Join(workspacesDir, ".keep")
	require.NoError(t, os.MkdirAll(dotDir, 0o750))

	SweepOrphanWorkspaceDirs(testDefaultContext)

	healthyDir := filepath.Join(workspacesDir, "healthy")
	require.DirExists(t, healthyDir)
	require.NoDirExists(t, orphanDir)
	require.DirExists(t, dotDir)
}

func TestSweepOrphanWorkspaceDirs_MissingDirIsNoop(t *testing.T) {
	setupTestPathManager(t)

	// No workspaces dir created yet — sweep must not panic or error.
	SweepOrphanWorkspaceDirs(testDefaultContext)
}

func TestSweepOrphanContentDirs_RemovesContentWithNoMatchingWorkspace(t *testing.T) {
	setupTestPathManager(t)

	contentsDir, err := provider.GetWorkspaceContentsDir(testDefaultContext)
	require.NoError(t, err)
	workspacesDir, err := provider.GetWorkspacesDir(testDefaultContext)
	require.NoError(t, err)

	// "orphaned" has content but no matching workspace dir at all.
	orphanedContent := filepath.Join(contentsDir, "orphaned")
	require.NoError(t, os.MkdirAll(orphanedContent, 0o750))

	// "kept" has both a content dir and a matching workspace dir — must survive.
	keptContent := filepath.Join(contentsDir, "kept")
	require.NoError(t, os.MkdirAll(keptContent, 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(workspacesDir, "kept"), 0o750))

	SweepOrphanContentDirs(testDefaultContext)

	require.NoDirExists(t, orphanedContent)
	require.DirExists(t, keptContent)
}

func TestSweepOrphanContentDirs_MissingDirIsNoop(t *testing.T) {
	setupTestPathManager(t)
	SweepOrphanContentDirs(testDefaultContext)
}

func TestDeleteWithPreResolvedClientSkipsSelection(t *testing.T) {
	setupTestPathManager(t)

	fake := &preResolvedDeleteClient{
		workspaceID: "chosen",
		config:      &provider.Workspace{ID: "chosen", Context: testDefaultContext},
	}
	devsyConfig := &config.Config{
		DefaultContext: testDefaultContext,
		Contexts:       map[string]*config.ContextConfig{testDefaultContext: {}},
	}

	id, err := Delete(t.Context(), DeleteOptions{
		DevsyConfig: devsyConfig,
		Client:      fake,
		Force:       true,
	})
	require.NoError(t, err)
	require.Equal(t, "chosen", id)
	require.True(t, fake.deleted)
}

type preResolvedDeleteClient struct {
	client.BaseWorkspaceClient
	workspaceID string
	config      *provider.Workspace
	deleted     bool
}

func (c *preResolvedDeleteClient) Workspace() string { return c.workspaceID }
func (c *preResolvedDeleteClient) WorkspaceConfig() *provider.Workspace {
	return c.config
}
func (c *preResolvedDeleteClient) Context() string  { return c.config.Context }
func (c *preResolvedDeleteClient) Provider() string { return DefaultDockerCommand }
func (c *preResolvedDeleteClient) Delete(context.Context, client.DeleteOptions) error {
	c.deleted = true
	return nil
}

func TestDeleteQuiescesPersistedUpTaskBeforeDeleting(t *testing.T) {
	setupTestPathManager(t)

	store, err := task.NewStore()
	require.NoError(t, err)
	tk, err := store.Create(task.CreateOptions{Command: "up", WorkspaceID: "chosen"})
	require.NoError(t, err)

	fake := &preResolvedDeleteClient{
		workspaceID: "chosen",
		config:      &provider.Workspace{ID: "chosen", Context: testDefaultContext},
	}
	devsyConfig := &config.Config{
		DefaultContext: testDefaultContext,
		Contexts:       map[string]*config.ContextConfig{testDefaultContext: {}},
	}

	id, err := Delete(t.Context(), DeleteOptions{
		DevsyConfig: devsyConfig,
		Client:      fake,
		Force:       true,
	})
	require.NoError(t, err)
	require.Equal(t, "chosen", id)
	require.True(t, fake.deleted)

	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	require.Equal(t, task.StatusFailed, state.Status)
	require.Equal(t, task.ErrCanceled.Error(), state.Error)
}

func TestDeleteBlockedWhenActiveUpTaskCannotBeQuiesced(t *testing.T) {
	setupTestPathManager(t)

	fake := &preResolvedDeleteClient{
		workspaceID: "chosen",
		config:      &provider.Workspace{ID: "chosen", Context: testDefaultContext},
	}
	devsyConfig := &config.Config{
		DefaultContext: testDefaultContext,
		Contexts:       map[string]*config.ContextConfig{testDefaultContext: {}},
	}

	_, err := Delete(t.Context(), DeleteOptions{
		DevsyConfig: devsyConfig,
		Client:      fake,
		Force:       true,
		quiesceUpTasks: func(string) error {
			return errors.New("boom")
		},
	})
	require.Error(t, err)
	require.False(t, fake.deleted, "delete must not proceed while a live up task survives")
}
