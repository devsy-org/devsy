// Package status defines the structured progress events emitted by
// long-running commands.
package status

// Phase identifies a step in the up pipeline.
type Phase string

const (
	PhaseCloningRepository    Phase = "cloning_repository"
	PhaseResolvingConfig      Phase = "resolving_config"
	PhaseInitializeCommand    Phase = "initialize_command"
	PhaseBuildingImage        Phase = "building_image"
	PhaseStartingContainer    Phase = "starting_container"
	PhaseInjectingAgent       Phase = "injecting_agent"
	PhaseRunningLifecycleHook Phase = "running_lifecycle_hook"
	PhaseWaitingFor           Phase = "waiting_for"
	PhaseReady                Phase = "ready"
	PhaseFailed               Phase = "failed"
)

// Event is one phase transition.
type Event struct {
	Phase   Phase  `json:"phase"`
	Step    string `json:"step,omitempty"`
	Started bool   `json:"started"`
	Err     string `json:"error,omitempty"`
}

// Reporter receives status events as they occur. Implementations must be
// safe to call from goroutines.
type Reporter interface {
	Report(Event)
}

func Enter(r Reporter, phase Phase, step string) {
	r.Report(Event{Phase: phase, Step: step, Started: true})
}

func Leave(r Reporter, phase Phase, step string) {
	r.Report(Event{Phase: phase, Step: step, Started: false})
}

func Fail(r Reporter, phase Phase, err error) {
	if err == nil {
		return
	}
	r.Report(Event{Phase: PhaseFailed, Step: string(phase), Err: err.Error()})
}

type nopReporter struct{}

func (nopReporter) Report(Event) {}

func Nop() Reporter { return nopReporter{} }

type teeReporter []Reporter

func (t teeReporter) Report(e Event) {
	for _, r := range t {
		r.Report(e)
	}
}

// Tee forwards every event to each reporter.
func Tee(reporters ...Reporter) Reporter {
	return teeReporter(reporters)
}
