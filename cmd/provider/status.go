package provider

import (
	"io"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/status"
)

// newStatusReporter drives provider progress output: one NDJSON status line
// per phase transition in JSON mode, human-readable info lines otherwise.

func newStatusReporter(
	resultFormat string,
	out io.Writer,
	verbose ...bool,
) (status.Reporter, error) {
	showDurations := len(verbose) > 0 && verbose[0]
	r, err := status.NewReporter(status.ReporterOptions{
		Format:                 resultFormat,
		Out:                    out,
		Prefix:                 "provider",
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
	return status.ForPipeline(r, status.PipelineProvider), nil
}

var phaseLabels = map[status.Phase]string{
	status.PhaseInstallingProvider: "installing provider",
	status.PhaseResolvingOptions:   "resolving options",
	status.PhaseRunningInit:        "running provider init",
	status.PhaseReady:              "ready",
}
