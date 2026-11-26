package workspace

import (
	"io"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/status"
)

const workspaceStatusPrefix = "workspace"

// newWorkspaceStatusReporter centralizes semantic progress presentation for
// workspace commands that expose a result stream.
func newWorkspaceStatusReporter(
	resultFormat string,
	out io.Writer,
	verbose bool,
	noStatus bool,
) (status.Reporter, error) {
	return status.NewCLIReporter(status.CLIOptions{
		Format:   resultFormat,
		Out:      out,
		Prefix:   workspaceStatusPrefix,
		Verbose:  verbose,
		NoStatus: noStatus,
		Pipeline: status.PipelineWorkspaceUp,
		Labels: map[status.Phase]string{
			status.PhaseRunningCommand:      "running command",
			status.PhaseBuildingImage:       "building devcontainer",
			status.PhaseStoppingWorkspace:   "stopping workspace",
			status.PhaseDeletingWorkspace:   "deleting workspace",
			status.PhaseRebuildingWorkspace: "rebuilding workspace",
			status.PhaseResettingWorkspace:  "resetting workspace",
			status.PhaseImportingWorkspace:  "importing workspace",
			status.PhaseReady:               "ready",
		},
		Envelope: func(e status.Event) error {
			return config2.WriteStatusJSON(out, e)
		},
	})
}
