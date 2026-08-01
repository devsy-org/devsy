// Package status defines the structured progress events emitted by
// long-running commands.
package status

// Pipeline identifies which command's progress an Event describes. Consumers
// share one event stream, so phase names alone are ambiguous: without this a
// sink cannot tell a provider install from a workspace up.
type Pipeline string

const (
	PipelineWorkspaceUp Pipeline = "workspace_up"
	PipelineProvider    Pipeline = "provider"
)

// Phase identifies a step in a pipeline. PhaseReady and PhaseFailed are
// shared terminal phases; the rest belong to one pipeline.
type Phase string

// Workspace up phases.
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

// Provider phases. Installing covers source resolution and binary download;
// ResolvingOptions and RunningInit are the two halves of provider init, split
// because only the latter executes provider-supplied code.
const (
	PhaseInstallingProvider Phase = "installing_provider"
	PhaseResolvingOptions   Phase = "resolving_options"
	PhaseRunningInit        Phase = "running_init"
)

// Event is one phase transition.
type Event struct {
	// Pipeline is stamped by ForPipeline rather than at each call site, so it
	// is empty on a directly-constructed event. Consumers should treat empty
	// as PipelineWorkspaceUp, which is what pre-discriminator CLIs emitted.
	Pipeline Pipeline `json:"pipeline,omitempty"`
	Phase    Phase    `json:"phase"`
	Step     string   `json:"step,omitempty"`
	Started  bool     `json:"started"`
	Err      string   `json:"error,omitempty"`
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

// ForPipeline stamps every event a reporter receives with pipeline, so the
// Enter/Leave/Fail helpers stay pipeline-agnostic and each producer declares
// its pipeline once where it builds its reporter.
func ForPipeline(r Reporter, pipeline Pipeline) Reporter {
	return pipelineReporter{next: r, pipeline: pipeline}
}

type pipelineReporter struct {
	next     Reporter
	pipeline Pipeline
}

func (p pipelineReporter) Report(e Event) {
	e.Pipeline = p.pipeline
	p.next.Report(e)
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
