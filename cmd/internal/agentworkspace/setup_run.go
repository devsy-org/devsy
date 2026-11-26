package agentworkspace

import (
	"os"
	"slices"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
)

// startProviderRun joins the run the CLI options name, in the provider host's
// own store, so initialization and image builds that run here are retained.
// The run reference is validated against the resolved workspace. It returns
// nil when no run is requested or history cannot be kept.
func startProviderRun(
	info *provider.AgentWorkspaceInfo,
	secretEntries []string,
) *workspacelogs.Session {
	opts := info.CLIOptions
	ws := info.Workspace
	if !workspacelogs.ValidID(opts.SetupRunID) || ws == nil {
		return nil
	}
	parent := ""
	if workspacelogs.ValidID(opts.SetupParentProducerID) {
		parent = opts.SetupParentProducerID
	}

	return workspacelogs.StartSession(workspacelogs.SessionOptions{
		RunID:            opts.SetupRunID,
		ParentProducerID: parent,
		Location:         workspacelogs.LocationProviderHost,
		Redactor: secrets.Combine(
			secrets.NewEnvironmentRedactor(os.Environ()),
			secrets.NewRedactor(slices.Clone(secretEntries)),
		),
		Run: workspacelogs.RunSpec{
			Context:            ws.Context,
			WorkspaceUID:       ws.UID,
			WorkspaceIDAtStart: ws.ID,
			Command:            "up",
		},
	})
}
