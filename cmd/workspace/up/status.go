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
	return status.NewCLIReporter(status.CLIOptions{
		Format:   resultFormat,
		Out:      out,
		Prefix:   "up",
		Verbose:  showDurations,
		Pipeline: status.PipelineWorkspaceUp,
		Labels:   phaseLabels,
		Envelope: func(e status.Event) error {
			return config2.WriteStatusJSON(out, e)
		},
	})
}

var phaseLabels = map[status.Phase]string{
	status.PhaseCloningRepository:     "cloning repository",
	status.PhaseResolvingConfig:       "resolving devcontainer config",
	status.PhaseInitializeCommand:     "running initializeCommand",
	status.PhasePreparingDevContainer: "preparing devcontainer",
	status.PhaseBuildingImage:         "building image",
	status.PhaseStartingContainer:     "starting container",
	status.PhaseInjectingAgent:        "injecting agent",
	status.PhaseRunningLifecycleHook:  "running lifecycle hooks",
	status.PhaseWaitingFor:            "waiting for readiness",
	status.PhaseConfiguringWorkspace:  "configuring workspace",
	status.PhaseConfiguringSSH:        "configuring SSH",
	status.PhaseStartingSSHTunnel:     "starting SSH tunnel",
	status.PhaseLaunchingIDE:          "launching IDE",
	status.PhaseReady:                 "ready",
}
