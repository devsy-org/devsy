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

func phaseLabel(p status.Phase) string {
	switch p {
	case status.PhaseCloningRepository:
		return "cloning repository"
	case status.PhaseResolvingConfig:
		return "resolving devcontainer config"
	case status.PhaseInitializeCommand:
		return "running initializeCommand"
	case status.PhaseBuildingImage:
		return "building image"
	case status.PhaseStartingContainer:
		return "starting container"
	case status.PhaseInjectingAgent:
		return "injecting agent"
	case status.PhaseRunningLifecycleHook:
		return "running lifecycle hooks"
	case status.PhaseWaitingFor:
		return "waiting for readiness"
	case status.PhaseReady:
		return "ready"
	default:
		return string(p)
	}
}
