package status

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/clierr"
)

type recordingReporter struct {
	events []Event
}

func (r *recordingReporter) Report(e Event) {
	r.events = append(r.events, e)
}

func TestEnterLeave(t *testing.T) {
	r := &recordingReporter{}
	Enter(r, PhaseBuildingImage, "")
	Leave(r, PhaseBuildingImage, "")

	if len(r.events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(r.events))
	}
	if r.events[0].State != StateStarted || r.events[0].Phase != PhaseBuildingImage {
		t.Errorf("unexpected enter event: %+v", r.events[0])
	}
	if r.events[1].State != StateSucceeded || r.events[1].Phase != PhaseBuildingImage {
		t.Errorf("unexpected leave event: %+v", r.events[1])
	}
}

func TestFail(t *testing.T) {
	r := &recordingReporter{}
	Fail(r, PhaseRunningLifecycleHook, errors.New("boom"))

	if len(r.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(r.events))
	}
	got := r.events[0]
	wantStep := string(PhaseRunningLifecycleHook)
	if got.Phase != PhaseFailed || got.State != StateFailed || got.Error == nil || got.Error.Message != "boom" || got.Step != wantStep {
		t.Errorf("unexpected fail event: %+v", got)
	}
}

func TestFailNilErrorIsNoop(t *testing.T) {
	r := &recordingReporter{}
	Fail(r, PhaseBuildingImage, nil)

	if len(r.events) != 0 {
		t.Errorf("expected no event for nil error, got %+v", r.events)
	}
}

func TestSkipReportsSkippedEvent(t *testing.T) {
	r := &recordingReporter{}
	Skip(r, PhaseInitializeCommand, "recovery mode")

	if len(r.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(r.events))
	}
	if got := r.events[0]; got.State != StateSkipped || got.Phase != PhaseInitializeCommand || got.Step != "recovery mode" {
		t.Errorf("unexpected skip event: %+v", got)
	}
}

func TestErrorFromPreservesActionableContext(t *testing.T) {
	err := &clierr.CLIError{
		Code:    clierr.CodeUnknown,
		Message: "Docker daemon is unavailable.",
		Hint:    "Start Docker and retry.",
		Context: map[string]string{"context": "desktop-linux"},
	}
	got := ErrorFrom(err)
	if got == nil || got.Code != string(clierr.CodeUnknown) || got.Context["context"] != "desktop-linux" {
		t.Fatalf("ErrorFrom lost context: %+v", got)
	}
}

func TestNopDiscardsEvents(t *testing.T) {
	Enter(Nop(), PhaseReady, "")
}

func TestMemoryReporterCollectsConcurrentEvents(t *testing.T) {
	r := NewMemoryReporter()
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() { r.Report(Event{Phase: PhaseReady}) })
	}
	wg.Wait()
	if got := len(r.Events()); got != 10 {
		t.Fatalf("collected %d events, want 10", got)
	}
}

func TestMemoryReporterDeepCopiesEvents(t *testing.T) {
	r := NewMemoryReporter()
	original := Event{
		Phase: PhaseBuildingImage,
		Error: &ErrorInfo{Message: "before", Context: map[string]string{"key": "before"}}, //nolint:goconst // snapshot isolation fixture
	}
	r.Report(original)
	original.Error.Message = "mutated"
	original.Error.Context["key"] = "mutated"

	got := r.Events()
	got[0].Error.Message = "changed snapshot"
	got[0].Error.Context["key"] = "changed snapshot"
	again := r.Events()
	if again[0].Error.Message != "before" || again[0].Error.Context["key"] != "before" {
		t.Fatalf("memory reporter did not isolate event mutations: %+v", again[0])
	}
}

func TestEnvelopeReporterSerializesConcurrentEvents(t *testing.T) {
	var mu sync.Mutex
	var events []Event
	r := NewEnvelopeReporter(func(e Event) error {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, e)
		return nil
	})
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() { r.Report(Event{Phase: PhaseReady}) })
	}
	wg.Wait()
	if len(events) != 10 {
		t.Fatalf("serialized %d events, want 10", len(events))
	}
}

func TestForPipelineStampsEveryEvent(t *testing.T) {
	r := &recordingReporter{}
	stamped := ForPipeline(r, PipelineProvider)

	Enter(stamped, PhaseBuildingImage, "")
	Fail(stamped, PhaseBuildingImage, errors.New("boom"))

	if len(r.events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(r.events))
	}
	for i, e := range r.events {
		if e.Pipeline != PipelineProvider {
			t.Errorf("event %d: pipeline = %q, want %q", i, e.Pipeline, PipelineProvider)
		}
	}
}

func TestForPipelineOverridesInboundPipeline(t *testing.T) {
	r := &recordingReporter{}
	ForPipeline(r, PipelineProvider).Report(Event{
		Phase:    PhaseReady,
		Pipeline: PipelineWorkspaceUp,
	})

	if r.events[0].Pipeline != PipelineProvider {
		t.Errorf("pipeline = %q, want it restamped to %q", r.events[0].Pipeline, PipelineProvider)
	}
}

func TestTeeForwardsToEachReporter(t *testing.T) {
	a, b := &recordingReporter{}, &recordingReporter{}
	Enter(Tee(a, b), PhaseReady, "")

	if len(a.events) != 1 || len(b.events) != 1 {
		t.Errorf("expected both reporters to receive the event: a=%+v b=%+v", a.events, b.events)
	}
}

func TestRunReportsLifecycleAndParent(t *testing.T) { //nolint:cyclop // validates multiple lifecycle invariants
	r := &recordingReporter{}
	err := Run(context.Background(), r, Operation{Phase: PhaseBuildingImage, Step: "image"}, //nolint:lll // lifecycle callback fixture
		func(ctx context.Context) error {
			if ParentOperationID(ctx) == "" {
				t.Fatal("child context has no operation ID")
			}
			return nil
		})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(r.events) != 2 {
		t.Fatalf("got %d events, want 2", len(r.events))
	}
	start, done := r.events[0], r.events[1]
	if start.State != StateStarted || start.OperationID == "" {
		t.Errorf("unexpected start event: %+v", start)
	}
	if done.State != StateSucceeded || done.OperationID != start.OperationID || done.Duration < 0 {
		t.Errorf("unexpected completion event: %+v", done)
	}
}

func TestRunReportsFailureAndNestedParent(t *testing.T) { //nolint:cyclop // validates nested failure lifecycle invariants
	r := &recordingReporter{}
	wantErr := errors.New("boom")
	err := Run(context.Background(), r, Operation{Phase: PhaseBuildingImage}, func(ctx context.Context) error {
		return Run(ctx, r, Operation{Phase: PhaseStartingContainer}, func(context.Context) error { return wantErr })
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run error = %v, want original error", err)
	}
	if len(r.events) != 4 {
		t.Fatalf("got %d events, want 4", len(r.events))
	}
	outerStart, innerStart, innerDone, outerDone := r.events[0], r.events[1], r.events[2], r.events[3]
	if innerStart.ParentOperationID != outerStart.OperationID {
		t.Errorf("inner parent = %q, want %q", innerStart.ParentOperationID, outerStart.OperationID)
	}
	if innerDone.State != StateFailed || innerDone.Error == nil || innerDone.Error.Message != wantErr.Error() {
		t.Errorf("unexpected inner failure: %+v", innerDone)
	}
	if outerDone.State != StateFailed || outerDone.Error == nil || outerDone.Error.Message != wantErr.Error() {
		t.Errorf("unexpected outer failure: %+v", outerDone)
	}
}

func TestRunDurationIsMeasured(t *testing.T) {
	r := &recordingReporter{}
	_ = Run(context.Background(), r, Operation{Phase: PhaseReady}, func(context.Context) error {
		time.Sleep(time.Millisecond)
		return nil
	})
	if r.events[1].Duration <= 0 {
		t.Errorf("duration = %v, want positive", r.events[1].Duration)
	}
}

func TestRunPreservesCancellationAndReportsFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &recordingReporter{}
	err := Run(ctx, r, Operation{Phase: PhaseWaitingFor}, func(ctx context.Context) error {
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context cancellation", err)
	}
	if len(r.events) != 2 || r.events[1].State != StateFailed || r.events[1].Error == nil {
		t.Fatalf("unexpected cancellation events: %+v", r.events)
	}
}

func TestRunConcurrentOperationsHaveUniqueIDs(t *testing.T) {
	r := NewMemoryReporter()
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			_ = Run(context.Background(), r, Operation{Phase: PhaseReady}, func(context.Context) error { return nil })
		})
	}
	wg.Wait()

	seen := make(map[string]struct{}, 32)
	for _, event := range r.Events() {
		if event.OperationID == "" {
			t.Fatal("operation event has no ID")
		}
		seen[event.OperationID] = struct{}{}
	}
	if len(seen) != 32 {
		t.Fatalf("got %d unique operation IDs, want 32", len(seen))
	}
}

func TestRunReportsPanicBeforeRethrowing(t *testing.T) {
	r := &recordingReporter{}
	func() {
		defer func() {
			if recover() != "boom" {
				t.Fatal("Run did not rethrow the original panic")
			}
		}()
		_ = Run(context.Background(), r, Operation{Phase: PhaseBuildingImage}, func(context.Context) error {
			panic("boom")
		})
	}()
	if len(r.events) != 2 || r.events[1].State != StateFailed || r.events[1].Error == nil || !strings.Contains(r.events[1].Error.Message, "boom") {
		t.Fatalf("unexpected panic event: %+v", r.events)
	}
}

func TestPlainReporterUsesASCIILifecycleMarkers(t *testing.T) {
	var buf bytes.Buffer
	r := NewPlainReporter(&buf, "up", map[Phase]string{PhaseBuildingImage: "Build image"})
	r.Report(Event{Phase: PhaseBuildingImage, State: StateStarted})
	r.Report(Event{Phase: PhaseBuildingImage, State: StateSucceeded, Duration: 1500 * time.Millisecond})
	r.Report(Event{Phase: PhaseBuildingImage, State: StateFailed, Error: &ErrorInfo{
		Code: "docker_daemon_unreachable", Message: "Docker daemon is unavailable.", Hint: "Start Docker and retry.",
		Context: map[string]string{"endpoint": "unix:///var/run/docker.sock"},
	}})
	got := buf.String()
	want := "[RUN]  up: Build image\n[OK]   up: Build image (1.5s)\n[FAIL] up: Build image\n       Docker daemon is unavailable.\n       Error code: docker_daemon_unreachable\n       Context:\n         endpoint: unix:///var/run/docker.sock\n       Try: Start Docker and retry.\n" //nolint:lll // exact rendered output fixture
	if got != want {
		t.Errorf("plain output = %q, want %q", got, want)
	}
}

func TestPlainReporterRedactsEnvironmentSecrets(t *testing.T) {
	t.Setenv("DEVSY_TEST_TOKEN", "status-secret")
	var buf bytes.Buffer
	r := NewPlainReporter(&buf, "up", nil)
	r.Report(Event{
		Phase: PhaseFailed,
		State: StateFailed,
		Error: &ErrorInfo{Message: "failed with status-secret", Hint: "retry status-secret"},
	})
	if strings.Contains(buf.String(), "status-secret") {
		t.Fatalf("secret escaped plain status output: %q", buf.String())
	}
}
