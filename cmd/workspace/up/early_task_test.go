package up

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func isolatedTaskStore(t *testing.T) *task.Store {
	t.Helper()
	t.Setenv("DEVSY_HOME", t.TempDir())
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)
	store, err := task.NewStore()
	require.NoError(t, err)
	return store
}

func TestDetachedRejectsInvalidIDBeforeCreatingTask(t *testing.T) {
	for _, id := range []string{"Backend", "backend_name", "backend.api", "has spaces", strings.Repeat("a", 49)} {
		t.Run(id, func(t *testing.T) {
			store := isolatedTaskStore(t)
			cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
			cmd.ID = id
			cmd.ResultFormat = formatJSON
			err := cmd.runDetached(nil, &config.Config{})
			assert.ErrorContains(t, err, "workspace name")
			tasks, err := store.List()
			require.NoError(t, err)
			require.Empty(t, tasks)
		})
	}
}

func TestWorkerRecordsValidationFailureBeforeRun(t *testing.T) {
	store := isolatedTaskStore(t)
	tk, err := store.Create(task.CreateOptions{Command: "up"})
	require.NoError(t, err)
	require.NoError(t, tk.SetPID(os.Getpid()))
	cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}, taskID: tk.ID()}
	cmd.WorkspaceMountConsistency = "invalid"
	cobraCmd := &cobra.Command{}
	cobraCmd.SetContext(context.Background())
	err = cmd.execute(cobraCmd, nil)
	require.ErrorContains(t, err, "workspace-mount-consistency")
	state, getErr := store.Get(tk.ID())
	require.NoError(t, getErr)
	require.Equal(t, task.StatusFailed, state.Status)
	require.Contains(t, state.Error, "workspace-mount-consistency")
	assertWorkerLockReleased(t, store, tk.ID())
}

func TestDuplicateWorkerCannotFailOwnerTask(t *testing.T) {
	store := isolatedTaskStore(t)
	tk, err := store.Create(task.CreateOptions{Command: "up"})
	require.NoError(t, err)
	require.NoError(t, tk.SetPID(os.Getpid()))
	require.NoError(t, tk.HoldWorkerLock())
	defer func() { require.NoError(t, tk.ReleaseWorkerLock()) }()
	cmd := &UpCmd{taskID: tk.ID()}
	_, err = cmd.openTask()
	require.ErrorContains(t, err, "already has a running worker")
	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	require.Equal(t, task.StatusPending, state.Status)
}

func TestDetachedValidatesMergedID(t *testing.T) {
	isolatedTaskStore(t)
	t.Setenv(config.EnvFlagsUp, `{"id":"Backend"}`)
	cmd := &UpCmd{}
	cmd.ID = "backend"
	require.ErrorContains(t, cmd.prepareDetached(nil), "lowercase")
	require.Equal(t, "Backend", cmd.ID)
}

func TestWorkerRecordsConfigAndClientErrors(t *testing.T) {
	for _, stage := range []string{"config", "client", "provider"} {
		t.Run(stage, func(t *testing.T) {
			store := isolatedTaskStore(t)
			tk, err := store.Create(task.CreateOptions{Command: "up"})
			require.NoError(t, err)
			require.NoError(t, tk.SetPID(os.Getpid()))
			cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}, taskID: tk.ID()}
			cmd.ResultFormat = formatJSON
			expected := "prepare workspace client"
			switch stage {
			case "config":
				path, pathErr := config.GetConfigPath()
				require.NoError(t, pathErr)
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
				require.NoError(t, os.WriteFile(path, []byte("contexts: ["), 0o600))
				expected = "load devsy config"
			case "client":
				cmd.ID = "Backend"
			case "provider":
				cmd.Provider = "missing-provider"
			}
			cobraCmd := &cobra.Command{}
			cobraCmd.SetContext(context.Background())
			err = cmd.execute(cobraCmd, []string{t.TempDir()})
			require.ErrorContains(t, err, expected)
			state, getErr := store.Get(tk.ID())
			require.NoError(t, getErr)
			require.Equal(t, task.StatusFailed, state.Status)
			require.Contains(t, state.Error, expected)
			assertWorkerLockReleased(t, store, tk.ID())
		})
	}
}

func TestFinalizeWorkerPreservesClassifiedRedactedError(t *testing.T) {
	store := isolatedTaskStore(t)
	testValue := strings.Repeat("test-value-", 8)
	t.Setenv("DEVSY_SECRETS_PASSPHRASE", testValue)
	tk, err := store.Create(task.CreateOptions{Command: "up"})
	require.NoError(t, err)
	require.NoError(t, tk.HoldWorkerLock())
	original := &clierr.CLIError{
		Code:    clierr.CodeCanceled,
		Message: testValue,
		Hint:    "retry " + testValue,
		Context: map[string]string{"source": testValue},
	}
	require.ErrorIs(t, finalizeWorkerTask(tk, original), original)
	second := errors.New("second failure")
	require.ErrorIs(t, finalizeWorkerTask(tk, second), second)
	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	require.Equal(t, string(clierr.CodeCanceled), state.ErrorCode)
	require.NotContains(t, state.Error, testValue)
	require.NotContains(t, state.ErrorHint, testValue)
	require.NotContains(t, state.ErrorContext["source"], testValue)
	assertWorkerLockReleased(t, store, tk.ID())
}

func TestDetachedAllowsOmittedAndMaximumLengthID(t *testing.T) {
	for _, id := range []string{"", strings.Repeat("a", 48)} {
		t.Run(id, func(t *testing.T) {
			isolatedTaskStore(t)
			cfg, err := config.LoadConfig("", "")
			require.NoError(t, err)
			cmd := &UpCmd{GlobalFlags: &flags.GlobalFlags{}}
			cmd.ID = id
			require.NoError(t, cmd.prepareDetached(cfg))
			require.Equal(t, id, cmd.ID)
		})
	}
}

func TestFinalizeWorkerSurfacesPersistenceFailure(t *testing.T) {
	store := isolatedTaskStore(t)
	tk, err := store.Create(task.CreateOptions{Command: "up"})
	require.NoError(t, err)
	require.NoError(t, tk.HoldWorkerLock())
	dir, err := config.DefaultPathManager().TaskDir()
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(dir, tk.ID()+".json")))
	original := errors.New("original worker failure")
	err = finalizeWorkerTask(tk, original)
	require.ErrorIs(t, err, original)
	require.ErrorContains(t, err, "record detached task failure")
	assertWorkerLockReleased(t, store, tk.ID())
}
