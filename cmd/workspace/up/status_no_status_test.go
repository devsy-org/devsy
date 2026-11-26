package up

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
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
			for _, event := range []status.Event{
				{Phase: status.PhasePreparingDevContainer, State: status.StateStarted, OperationID: "parent"},
				{
					Phase: status.PhaseStartingContainer, State: status.StateSucceeded,
					OperationID: "child", ParentOperationID: "parent",
				},
			} {
				cmd.statusReporter.Report(event)
			}
			dir, err := workspacejournal.DefaultDir()
			require.NoError(t, err)
			events, err := workspacejournal.Read(dir, "test-workspace", 10)
			require.NoError(t, err)
			require.Len(t, events, 2)
			if worker {
				lines := strings.Split(strings.TrimSpace(out.String()), "\n")
				require.Len(t, lines, 2)
				completion, ok := devcconfig.ParseStatusLine(lines[1])
				require.True(t, ok)
				require.Equal(t, status.PhaseStartingContainer, completion.Phase)
				require.Equal(t, status.StateSucceeded, completion.State)
				require.Equal(t, "child", completion.OperationID)
				require.Equal(t, "parent", completion.ParentOperationID)
			} else {
				require.Empty(t, out.String())
			}
		})
	}
}
