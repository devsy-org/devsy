package ci

import (
	"io"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/status"
)

func newStatusReporter(
	format string,
	out io.Writer,
	verbose bool,
	noStatus bool,
) (status.Reporter, error) {
	return status.NewCLIReporter(status.CLIOptions{
		Format:   format,
		Out:      out,
		Prefix:   "ci",
		Verbose:  verbose,
		NoStatus: noStatus,
		Pipeline: status.PipelineWorkspaceUp,
		Labels: map[status.Phase]string{
			status.PhaseCloningRepository:     "cloning repository",
			status.PhaseResolvingConfig:       "resolving devcontainer config",
			status.PhaseInitializeCommand:     "running initializeCommand",
			status.PhasePreparingDevContainer: "preparing devcontainer",
			status.PhaseBuildingImage:         "building image",
			status.PhaseStartingContainer:     "starting container",
			status.PhaseInjectingAgent:        "injecting agent",
			status.PhaseRunningLifecycleHook:  "running lifecycle hooks",
			status.PhaseWaitingFor:            "waiting for readiness",
			status.PhaseReady:                 "ready",
			status.PhaseRunningCommand:        "running command",
			status.PhaseDeletingWorkspace:     "tearing down workspace",
		},
		Envelope: func(e status.Event) error { return config2.WriteStatusJSON(out, e) },
	})
}
