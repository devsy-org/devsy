package up

import (
	"io"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/status"
)

// newStatusReporter drives `up`'s progress output.
func newStatusReporter(
	resultFormat string,
	out io.Writer,
	verbose ...bool,
) (status.Reporter, error) {
	showDurations := len(verbose) > 0 && verbose[0]
	r, err := status.NewReporter(status.ReporterOptions{
		Format:                 resultFormat,
		Out:                    out,
		Prefix:                 "up",
		Labels:                 phaseLabels,
		Verbose:                showDurations,
		SuppressFailureDetails: true,
		Envelope: func(e status.Event) error {
			return config2.WriteStatusJSON(out, e)
		},
	})
	if err != nil {
		return nil, err
	}
	return status.ForPipeline(r, status.PipelineWorkspaceUp), nil
}

var phaseLabels = map[status.Phase]string{
	status.PhaseCloningRepository:    "cloning repository",
	status.PhaseResolvingConfig:      "resolving devcontainer config",
	status.PhaseInitializeCommand:    "running initializeCommand",
	status.PhaseBuildingImage:        "building image",
	status.PhaseStartingContainer:    "starting container",
	status.PhaseInjectingAgent:       "injecting agent",
	status.PhaseRunningLifecycleHook: "running lifecycle hooks",
	status.PhaseWaitingFor:           "waiting for readiness",
	status.PhaseConfiguringWorkspace: "configuring workspace",
	status.PhaseConfiguringSSH:       "configuring SSH",
	status.PhaseStartingSSHTunnel:    "starting SSH tunnel",
	status.PhaseLaunchingIDE:         "launching IDE",
	status.PhaseReady:                "ready",
}
