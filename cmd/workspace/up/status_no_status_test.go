package up

import (
	"bytes"
	"os"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/devsy-org/devsy/pkg/workspacejournal"
	"github.com/stretchr/testify/require"
)

const formatJSON = "json"

func TestConfigureRunNoStatusPreservesJournalAndWorkerRecording(t *testing.T) {
	for _, worker := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreground", true: "worker"}[worker], func(t *testing.T) {
			store := isolatedTaskStore(t)
			var out bytes.Buffer
			cmd := &UpCmd{
				GlobalFlags: &flags.GlobalFlags{NoStatus: true, ResultFormat: formatJSON},
				Out:         &out,
			}
			if worker {
				tk, err := store.Create(task.CreateOptions{Command: "up"})
				require.NoError(t, err)
				require.NoError(t, tk.SetPID(os.Getpid()))
				cmd.taskID = tk.ID()
				cmd.workerTask, err = cmd.openTask()
				require.NoError(t, err)
				require.NotNil(t, cmd.workerTask)
				t.Cleanup(func() { require.NoError(t, cmd.workerTask.ReleaseWorkerLock()) })
			}
			_, _, err := cmd.configureRun("test-workspace")
			require.NoError(t, err)
			if worker {
				_, err = cmd.setUpTask(
					presentationClient{ws: &provider.Workspace{ID: "test-workspace"}},
					true,
					&out,
				)
				require.NoError(t, err)
			}
			for _, event := range []status.Event{
				{Phase: status.PhasePreparingDevContainer, State: status.StateStarted, OperationID: "parent"},
				{
					Phase: status.PhaseStartingContainer, State: status.StateSucceeded,
					OperationID: "child", ParentOperationID: "parent",
				},
			} {
				cmd.statusReporter.Report(event)
				if worker {
					state, err := store.Get(cmd.workerTask.ID())
					require.NoError(t, err)
					require.Equal(t, string(event.Phase), state.Phase)
					require.Equal(t, event.OperationID, state.OperationID)
					require.Equal(t, event.ParentOperationID, state.ParentOperationID)
				}
			}
			dir, err := workspacejournal.DefaultDir()
			require.NoError(t, err)
			events, err := workspacejournal.Read(dir, "test-workspace", 10)
			require.NoError(t, err)
			require.Len(t, events, 2)
			require.Equal(t, status.StateStarted, events[0].State)
			require.Equal(t, status.PhasePreparingDevContainer, events[0].Phase)
			require.Equal(t, status.StateSucceeded, events[1].State)
			require.Equal(t, status.PhaseStartingContainer, events[1].Phase)
			require.Empty(t, out.String())
		})
	}
}
