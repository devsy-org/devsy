package ci

import (
	"io"

	"github.com/devsy-org/devsy/pkg/status"
)

func newStatusReporter(out io.Writer, verbose bool) (status.Reporter, error) {
	return status.NewCLIReporter(status.CLIOptions{
		Format:   "plain",
		Out:      out,
		Prefix:   "ci",
		Verbose:  verbose,
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
		Envelope: func(status.Event) error { return nil },
	})
}
