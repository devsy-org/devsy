package daemonclient

import (
	"bytes"
	"testing"

	"github.com/devsy-org/devsy/pkg/status"
)

type recordingReporter struct {
	events []status.Event
}

func (r *recordingReporter) Report(e status.Event) {
	r.events = append(r.events, e)
}

func TestStatusSniffingWriter_ForwardsPlainLogLines(t *testing.T) {
	var next bytes.Buffer
	reporter := &recordingReporter{}
	w := newStatusSniffingWriter(&next, reporter)

	_, err := w.Write([]byte("hello\nworld\n"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if got := next.String(); got != "hello\nworld\n" {
		t.Errorf("next = %q, want %q", got, "hello\nworld\n")
	}
	if len(reporter.events) != 0 {
		t.Errorf("expected no status events, got %d", len(reporter.events))
	}
}

func TestStatusSniffingWriter_ExtractsStatusLines(t *testing.T) {
	var next bytes.Buffer
	reporter := &recordingReporter{}
	w := newStatusSniffingWriter(&next, reporter)

	input := `before
{"kind":"status","phase":"building_image","started":true}
after
`
	if _, err := w.Write([]byte(input)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if got := next.String(); got != "before\nafter\n" {
		t.Errorf("next = %q, want %q", got, "before\nafter\n")
	}
	if len(reporter.events) != 1 {
		t.Fatalf("expected 1 status event, got %d", len(reporter.events))
	}
	e := reporter.events[0]
	if e.Phase != status.PhaseBuildingImage || !e.Started {
		t.Errorf("unexpected event: %+v", e)
	}
}

func TestStatusSniffingWriter_FlushesPartialLineOnClose(t *testing.T) {
	var next bytes.Buffer
	w := newStatusSniffingWriter(&next, &recordingReporter{})

	if _, err := w.Write([]byte("no newline yet")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if next.Len() != 0 {
		t.Errorf("expected nothing forwarded before close, got %q", next.String())
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if got := next.String(); got != "no newline yet" {
		t.Errorf("next = %q, want %q", got, "no newline yet")
	}
}

func TestStatusSniffingWriter_SplitAcrossWrites(t *testing.T) {
	var next bytes.Buffer
	reporter := &recordingReporter{}
	w := newStatusSniffingWriter(&next, reporter)

	if _, err := w.Write([]byte(`{"kind":"status","phase":"read`)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := w.Write([]byte(`y","started":false}` + "\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if next.Len() != 0 {
		t.Errorf("expected nothing forwarded, got %q", next.String())
	}
	if len(reporter.events) != 1 || reporter.events[0].Phase != status.PhaseReady {
		t.Errorf("unexpected events: %+v", reporter.events)
	}
}
