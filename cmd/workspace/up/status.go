package up

import (
	"io"

	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/status"
	"github.com/devsy-org/devsy/pkg/log"
)

// newStatusReporter drives `up`'s progress output: one NDJSON status line
// per phase transition in JSON mode, human-readable info lines otherwise.
func newStatusReporter(emitJSON bool, out io.Writer) status.Reporter {
	if emitJSON {
		return &jsonStatusReporter{out: out}
	}
	return plainStatusReporter{}
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
		log.Errorf("up: phase %q failed: %s", e.Step, e.Err)
	case e.Started && e.Step != "":
		log.Infof("up: %s: %s", phaseLabel(e.Phase), e.Step)
	case e.Started:
		log.Infof("up: %s", phaseLabel(e.Phase))
	}
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
	status.PhaseReady:                "ready",
}

func phaseLabel(p status.Phase) string {
	if label, ok := phaseLabels[p]; ok {
		return label
	}
	return string(p)
}
