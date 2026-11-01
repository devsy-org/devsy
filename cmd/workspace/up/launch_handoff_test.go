package up

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/stretchr/testify/require"
)

func TestDetachedWorkerCommitsBeforeWorkspaceWork(t *testing.T) {
	store := isolatedTaskStore(t)
	tk, err := store.CreateLaunch(task.CreateOptions{Command: "up"})
	require.NoError(t, err)
	err = command.StartSupervisedBackground(command.SupervisedStartOptions{
		Name: task.WorkerProcessName(tk.ID()), Lease: tk.LaunchLease(), AtomicOwnership: true,
		OnPrepared: tk.PrepareProcessOwnership,
		OnStarted: func(ref command.ProcessRef) error {
			if err := tk.PublishProcess(ref); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return tk.CommitLaunch(ctx)
		},
	}, func() (*exec.Cmd, error) {
		child := exec.Command(
			os.Args[0],
			"-test.run=^TestDetachedWorkerHandoffHelper$",
		) // #nosec G204 G702 -- re-exec this test binary
		child.Env = append(os.Environ(), "DEVSY_TEST_HANDOFF_ID="+tk.ID())
		return child, nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Cancel() })
	require.Eventually(t, func() bool {
		state, err := store.Get(tk.ID())
		return err == nil && state.Status == task.StatusSucceeded
	}, 10*time.Second, 20*time.Millisecond)
	state, err := store.Get(tk.ID())
	require.NoError(t, err)
	require.True(t, state.WorkerReady)
	require.False(t, state.LaunchPending)
}

func TestDetachedWorkerHandoffHelper(t *testing.T) {
	id := os.Getenv("DEVSY_TEST_HANDOFF_ID")
	if id == "" {
		return
	}
	cmd := &UpCmd{taskID: id}
	tk, err := cmd.openTask()
	require.NoError(t, err)
	require.NoError(t, tk.Succeed(nil))
	require.NoError(t, tk.ReleaseWorkerLock())
}
