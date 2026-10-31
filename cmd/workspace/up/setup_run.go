package up

import (
	"os"

	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
)

// startSetupRun starts or joins the run that retains this invocation's build
// and lifecycle output and installs its identity in the CLI options sent down
// the setup chain. A detached worker joins the run its submission allocated.
// A storage problem leaves history unrecorded and never fails the command.
func (cmd *UpCmd) startSetupRun(client client2.BaseWorkspaceClient) *workspacelogs.Session {
	// A run reference never carries over from earlier state.
	cmd.CLIOptions.SetupRunID = ""
	cmd.CLIOptions.SetupParentProducerID = ""

	spec := workspacelogs.RunSpec{
		Context:            client.Context(),
		WorkspaceIDAtStart: client.Workspace(),
		Command:            "up",
		TaskID:             cmd.taskID,
	}
	opts := workspacelogs.SessionOptions{
		Run:      spec,
		Location: workspacelogs.LocationHost,
		Redactor: secrets.NewEnvironmentRedactor(os.Environ()),
	}
	if cmd.workerTask != nil {
		opts.RunID = cmd.workerTask.RunID()
	}
	session := workspacelogs.StartSession(opts)
	if session == nil {
		return nil
	}
	if ws := client.WorkspaceConfig(); ws != nil {
		session.BindWorkspace(ws.UID, client.Workspace())
	}
	cmd.CLIOptions.SetupRunID = session.RunID
	cmd.CLIOptions.SetupParentProducerID = session.ProducerID()

	return session
}

// provisionalRun allocates the run for a detached submission before the
// workspace is resolved. Its workspace UID is bound later by the worker. It
// returns an empty ID when history cannot be recorded.
func provisionalRun(cfg *config.Config, workspaceID string) string {
	store, err := workspacelogs.OpenDefault()
	if err != nil {
		return ""
	}
	m, err := store.CreateRun(workspacelogs.RunSpec{
		Context:            cfg.DefaultContext,
		WorkspaceIDAtStart: workspaceID,
		Command:            "up",
	})
	if err != nil {
		return ""
	}

	return m.RunID
}

// linkRunToTask records the task that owns a provisional run, or marks the run
// failed when the task never launched.
func linkRunToTask(runID, taskID string, launchErr error) {
	if runID == "" {
		return
	}
	store, err := workspacelogs.OpenDefault()
	if err != nil {
		return
	}
	if launchErr != nil {
		_ = store.SetCommandState(runID, workspacelogs.CommandFailed)

		return
	}
	_ = store.SetTaskID(runID, taskID)
}
