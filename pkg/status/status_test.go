package status

import (
	"errors"
	"testing"
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
	if !r.events[0].Started || r.events[0].Phase != PhaseBuildingImage {
		t.Errorf("unexpected enter event: %+v", r.events[0])
	}
	if r.events[1].Started || r.events[1].Phase != PhaseBuildingImage {
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
	if got.Phase != PhaseFailed || got.Err != "boom" || got.Step != wantStep {
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

func TestNopDiscardsEvents(t *testing.T) {
	Enter(Nop(), PhaseReady, "")
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
