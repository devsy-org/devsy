package provider

import (
	"io"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/status"
)

// newStatusReporter drives provider progress output: one NDJSON status line
// per phase transition in JSON mode, human-readable info lines otherwise.
func newStatusReporter(resultFormat string, out io.Writer) (status.Reporter, error) {
	mode, err := output.ResolveMode(resultFormat)
	if err != nil {
		return nil, err
	}

	var r status.Reporter = plainStatusReporter{}
	if mode == output.ModeJSON {
		r = &jsonStatusReporter{out: out}
	}
	return status.ForPipeline(r, status.PipelineProvider), nil
}

type jsonStatusReporter struct {
	out io.Writer
}

func (r *jsonStatusReporter) Report(e status.Event) {
	_ = config2.WriteStatusJSON(r.out, e)
}

type plainStatusReporter struct{}

func (plainStatusReporter) Report(e status.Event) {
	switch {
	case e.Phase == status.PhaseFailed:
		log.Errorf("provider: phase %q failed: %s", e.Step, e.Err)
	case e.Started:
		log.Infof("provider: %s", phaseLabel(e.Phase))
	}
}

var phaseLabels = map[status.Phase]string{
	status.PhaseInstallingProvider: "installing provider",
	status.PhaseResolvingOptions:   "resolving options",
	status.PhaseRunningInit:        "running provider init",
	status.PhaseReady:              "ready",
}

func phaseLabel(p status.Phase) string {
	if label, ok := phaseLabels[p]; ok {
		return label
	}
	return string(p)
}
