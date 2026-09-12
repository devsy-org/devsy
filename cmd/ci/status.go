package ci

import (
	"io"

	"github.com/devsy-org/devsy/pkg/status"
)

// newStatusReporter creates the deterministic CI progress stream. Progress
// belongs on stderr so the command executed inside the container keeps stdout
// safe for piping and artifact capture.
func newStatusReporter(out io.Writer, verbose bool) (status.Reporter, error) {
	reporter, err := status.NewReporter(status.ReporterOptions{
		Format:                 "plain",
		Out:                    out,
		Prefix:                 "ci",
		Verbose:                verbose,
		SuppressFailureDetails: true,
		Labels: map[status.Phase]string{
			status.PhaseCloningRepository:    "cloning repository",
			status.PhaseResolvingConfig:      "resolving devcontainer config",
			status.PhaseInitializeCommand:    "running initializeCommand",
			status.PhaseBuildingImage:        "building image",
			status.PhaseStartingContainer:    "starting container",
			status.PhaseInjectingAgent:       "injecting agent",
			status.PhaseRunningLifecycleHook: "running lifecycle hooks",
			status.PhaseWaitingFor:           "waiting for readiness",
			status.PhaseReady:                "ready",
			status.PhaseRunningCommand:       "running command",
			status.PhaseDeletingWorkspace:    "tearing down workspace",
		},
		Envelope: func(status.Event) error { return nil },
	})
	if err != nil {
		return nil, err
	}
	return status.ForPipeline(reporter, status.PipelineWorkspaceUp), nil
}
