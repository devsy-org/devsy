package status

import (
	"bytes"
	"testing"
	"time"
)

func TestNewReporter_SelectsPlainAndEnvelope(t *testing.T) {
	var plain bytes.Buffer
	plainReporter, err := NewReporter(ReporterOptions{
		Format: formatPlain,
		Out:    &plain,
	})
	if err != nil {
		t.Fatalf("plain reporter: %v", err)
	}
	plainReporter.Report(Event{Phase: PhaseReady, State: StateSucceeded})
	if got := plain.String(); got != "[OK]   ready\n" {
		t.Fatalf("plain output = %q", got)
	}

	var events []Event
	jsonReporter, err := NewReporter(ReporterOptions{
		Format: formatJSON,
		Out:    &plain,
		Envelope: func(e Event) error {
			events = append(events, e)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("json reporter: %v", err)
	}
	jsonReporter.Report(Event{Phase: PhaseReady, State: StateStarted})
	if len(events) != 1 || events[0].State != StateStarted {
		t.Fatalf("events = %#v", events)
	}
}

func TestNewReporter_AutoUsesPlainForInteractive(t *testing.T) {
	var out bytes.Buffer
	reporter, err := NewReporter(ReporterOptions{
		Format:      formatAuto,
		Out:         &out,
		Interactive: true,
	})
	if err != nil {
		t.Fatalf("auto reporter: %v", err)
	}
	reporter.Report(Event{Phase: PhaseReady, State: StateSucceeded})
	if got := out.String(); got != "[OK]   ready\n" {
		t.Fatalf("auto output = %q", got)
	}
}

func TestNewReporter_VerboseShowsShortDurations(t *testing.T) {
	var out bytes.Buffer
	reporter, err := NewReporter(ReporterOptions{
		Format:  formatPlain,
		Out:     &out,
		Verbose: true,
	})
	if err != nil {
		t.Fatalf("verbose reporter: %v", err)
	}
	reporter.Report(Event{Phase: PhaseReady, State: StateSucceeded, Duration: 120 * time.Millisecond})
	if got := out.String(); got != "[OK]   ready (0.1s)\n" {
		t.Fatalf("verbose output = %q", got)
	}
}

func TestNewReporter_CanLeaveFailureDetailsToCommandBoundary(t *testing.T) {
	var out bytes.Buffer
	reporter, err := NewReporter(ReporterOptions{
		Format:                 "plain",
		Out:                    &out,
		SuppressFailureDetails: true,
	})
	if err != nil {
		t.Fatalf("plain reporter: %v", err)
	}
	reporter.Report(Event{
		Phase: PhaseBuildingImage,
		State: StateFailed,
		Error: &ErrorInfo{Code: "build_failed", Message: "secret implementation detail"},
	})
	if got := out.String(); got != "[FAIL] building_image\n" {
		t.Fatalf("failure output = %q", got)
	}
}

func TestNewReporter_AutoRequiresEnvelopeWhenNonInteractive(t *testing.T) {
	_, err := NewReporter(ReporterOptions{Format: "auto", Out: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("expected missing JSON envelope error")
	}
}

func TestNewReporter_DesktopForcesEnvelope(t *testing.T) {
	t.Setenv("DEVSY_UI", "true")
	var events []Event
	reporter, err := NewReporter(ReporterOptions{
		Format: "plain",
		Envelope: func(e Event) error {
			events = append(events, e)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("desktop reporter: %v", err)
	}
	reporter.Report(Event{Phase: PhaseReady, State: StateStarted})
	if len(events) != 1 {
		t.Fatalf("desktop events = %#v", events)
	}
}

func TestNewReporter_RejectsUnknownFormat(t *testing.T) {
	_, err := NewReporter(ReporterOptions{Format: "ansi", Out: &bytes.Buffer{}})
	if err == nil {
		t.Fatal("expected invalid format error")
	}
}

func TestHumanAndLogReportersHaveExplicitContracts(t *testing.T) {
	var out bytes.Buffer
	human := NewHumanReporter(&out, "", nil)
	if _, ok := human.(HumanReporter); !ok {
		t.Fatalf("human reporter has type %T, want status.HumanReporter", human)
	}
	human.Report(Event{Phase: PhaseReady, State: StateSucceeded})
	if out.Len() == 0 {
		t.Fatal("human reporter emitted no output")
	}

	if NewLogReporter() == nil {
		t.Fatal("log reporter is nil")
	}
}
