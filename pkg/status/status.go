// Package status defines the structured progress events emitted by
// long-running commands.
package status

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/devsy-org/devsy/pkg/clierr"
)

// Pipeline identifies which command's progress an Event describes.
type Pipeline string

const (
	PipelineWorkspaceUp Pipeline = "workspace_up"
	PipelineProvider    Pipeline = "provider"
)

// State describes the lifecycle transition represented by an Event.
type State string

const (
	StateStarted   State = "started"
	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateSkipped   State = "skipped"
)

// ValidState reports whether s is part of the current status protocol.
func ValidState(s State) bool {
	switch s {
	case StateStarted, StateSucceeded, StateFailed, StateSkipped:
		return true
	default:
		return false
	}
}

// ErrorInfo is the stable, user-facing portion of an operation failure.
// Implementation errors should not be serialized as part of the status
// protocol; callers may retain them in the returned error instead.
type ErrorInfo struct {
	Code    string            `json:"code,omitempty"`
	Message string            `json:"message"`
	Hint    string            `json:"hint,omitempty"`
	Context map[string]string `json:"context,omitempty"`
}

// Phase identifies a step in a pipeline. PhaseReady and PhaseFailed are
// shared terminal phases.
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
	PhaseRunningCommand       Phase = "running_command"
	PhaseConfiguringWorkspace Phase = "configuring_workspace"
	PhaseConfiguringSSH       Phase = "configuring_ssh"
	PhaseStartingSSHTunnel    Phase = "starting_ssh_tunnel"
	PhaseLaunchingIDE         Phase = "launching_ide"
	PhaseStoppingWorkspace    Phase = "stopping_workspace"
	PhaseDeletingWorkspace    Phase = "deleting_workspace"
	PhaseRebuildingWorkspace  Phase = "rebuilding_workspace"
	PhaseResettingWorkspace   Phase = "resetting_workspace"
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
	Pipeline          Pipeline      `json:"pipeline,omitempty"`
	OperationID       string        `json:"operationId,omitempty"`
	ParentOperationID string        `json:"parentOperationId,omitempty"`
	Phase             Phase         `json:"phase"`
	Step              string        `json:"step,omitempty"`
	State             State         `json:"state,omitempty"`
	Duration          time.Duration `json:"-"`
	Error             *ErrorInfo    `json:"error,omitempty"`
}

// Reporter receives status events as they occur. Implementations must be
// safe to call from goroutines.
type Reporter interface {
	Report(Event)
}

func Enter(r Reporter, phase Phase, step string) {
	if r == nil {
		return
	}
	r.Report(Event{Phase: phase, Step: step, State: StateStarted})
}

func Leave(r Reporter, phase Phase, step string) {
	if r == nil {
		return
	}
	r.Report(Event{Phase: phase, Step: step, State: StateSucceeded})
}

// Skip reports a phase that was intentionally not executed.
func Skip(r Reporter, phase Phase, step string) {
	if r == nil {
		return
	}
	r.Report(Event{Phase: phase, Step: step, State: StateSkipped})
}

func Fail(r Reporter, phase Phase, err error) {
	if r == nil || err == nil {
		return
	}
	r.Report(Event{Phase: PhaseFailed, Step: string(phase), State: StateFailed, Error: ErrorFrom(err)})
}

// ErrorFrom converts an implementation error into the stable status error
// shape without exposing its wrapped Go error chain in the protocol.
func ErrorFrom(err error) *ErrorInfo {
	if err == nil {
		return nil
	}
	classified := clierr.Classify(err)
	return &ErrorInfo{
		Code:    string(classified.Code),
		Message: classified.Message,
		Hint:    classified.Hint,
		Context: classified.Context,
	}
}

type operationContextKey struct{}

var operationSequence atomic.Uint64

// Operation identifies a scoped status operation.
type Operation struct {
	Pipeline Pipeline
	Phase    Phase
	Step     string
}

// Run reports a complete operation lifecycle and returns the callback's
// original error. Nested operations inherit their parent's operation ID.
func Run(ctx context.Context, reporter Reporter, operation Operation, fn func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if reporter == nil {
		reporter = Nop()
	}
	id := fmt.Sprintf("op-%d", operationSequence.Add(1))
	parent, _ := ctx.Value(operationContextKey{}).(string)
	started := Event{Pipeline: operation.Pipeline, OperationID: id, ParentOperationID: parent, Phase: operation.Phase, Step: operation.Step, State: StateStarted}
	reporter.Report(started)
	childCtx := context.WithValue(ctx, operationContextKey{}, id)
	start := time.Now()
	defer func() {
		if recovered := recover(); recovered != nil {
			reporter.Report(Event{
				Pipeline: operation.Pipeline, OperationID: id, ParentOperationID: parent,
				Phase: operation.Phase, Step: operation.Step, State: StateFailed,
				Duration: time.Since(start),
				Error:    ErrorFrom(fmt.Errorf("panic: %v", recovered)),
			})
			panic(recovered)
		}
	}()
	err := fn(childCtx)
	done := Event{Pipeline: operation.Pipeline, OperationID: id, ParentOperationID: parent, Phase: operation.Phase, Step: operation.Step, Duration: time.Since(start)}
	if err != nil {
		done.State = StateFailed
		done.Error = ErrorFrom(err)
	} else {
		done.State = StateSucceeded
	}
	reporter.Report(done)
	return err
}

// ParentOperationID returns the current operation ID, if any.
func ParentOperationID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(operationContextKey{}).(string)
	return id
}

// OperationID returns the current operation ID, if any.
func OperationID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(operationContextKey{}).(string)
	return id
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

// MemoryReporter collects status events for tests and embedding applications.
// It is safe for concurrent Report calls.
type MemoryReporter struct {
	mu     sync.Mutex
	events []Event
}

// EnvelopeReporter forwards events to an encoder supplied by the output
// layer. Keeping encoding injectable avoids coupling status to any envelope
// package while centralizing synchronization and reporter selection.
type EnvelopeReporter struct {
	mu    sync.Mutex
	write func(Event) error
}

// NewEnvelopeReporter creates a thread-safe structured-event reporter.
func NewEnvelopeReporter(write func(Event) error) Reporter {
	return &EnvelopeReporter{write: write}
}

func (r *EnvelopeReporter) Report(e Event) {
	if r == nil || r.write == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = r.write(e)
}

// NewMemoryReporter returns an empty concurrent event collector.
func NewMemoryReporter() *MemoryReporter { return &MemoryReporter{} }

func (r *MemoryReporter) Report(e Event) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.events = append(r.events, cloneEvent(e))
	r.mu.Unlock()
}

// Events returns a snapshot that callers may safely modify.
func (r *MemoryReporter) Events() []Event {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	events := make([]Event, len(r.events))
	for i, event := range r.events {
		events[i] = cloneEvent(event)
	}
	return events
}

func cloneEvent(e Event) Event {
	if e.Error == nil {
		return e
	}
	copy := *e.Error
	if e.Error.Context != nil {
		copy.Context = maps.Clone(e.Error.Context)
	}
	e.Error = &copy
	return e
}

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
