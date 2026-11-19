package status

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testTextFormat = "text"

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

func TestTextFormatRendersPlain(t *testing.T) {
	var out bytes.Buffer
	reporter, err := NewReporter(ReporterOptions{Format: testTextFormat, Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	reporter.Report(Event{Phase: PhaseReady, State: StateSucceeded})
	if out.String() != "[OK]   ready\n" {
		t.Fatalf("unexpected output %q", out.String())
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
	reporter.Report(
		Event{Phase: PhaseReady, State: StateSucceeded, Duration: 120 * time.Millisecond},
	)
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

func TestNewReporter_DesktopFormatPrecedence(t *testing.T) {
	t.Setenv("DEVSY_UI", "true")
	for _, format := range []string{formatPlain, testTextFormat} {
		var out bytes.Buffer
		reporter, err := NewReporter(ReporterOptions{Format: format, Out: &out, Interactive: true})
		if err != nil {
			t.Fatalf("%s reporter: %v", format, err)
		}
		reporter.Report(Event{Phase: PhaseReady, State: StateSucceeded})
		if got := out.String(); got != "[OK]   ready\n" {
			t.Fatalf("%s output = %q", format, got)
		}
	}
	var events []Event
	reporter, err := NewReporter(ReporterOptions{
		Format:      "auto",
		Interactive: true,
		Envelope: func(e Event) error {
			events = append(events, e)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("desktop auto reporter: %v", err)
	}
	reporter.Report(Event{Phase: PhaseReady, State: StateStarted})
	if len(events) != 1 {
		t.Fatalf("desktop events = %#v", events)
	}
}

func TestNewReporter_NoStatus(t *testing.T) {
	t.Setenv("DEVSY_UI", "")
	var out bytes.Buffer
	calls := 0
	for _, format := range []string{formatPlain, formatJSON} {
		reporter, err := NewReporter(ReporterOptions{
			Format: format, Out: &out, NoStatus: true,
			Envelope: func(Event) error { calls++; return nil },
		})
		if err != nil {
			t.Fatalf("%s reporter: %v", format, err)
		}
		reporter.Report(Event{Phase: PhaseReady, State: StateStarted})
	}
	if out.Len() != 0 || calls != 0 {
		t.Fatalf("suppressed output = %q, envelope calls = %d", out.String(), calls)
	}
	for _, opts := range []ReporterOptions{
		{Format: "ansi", NoStatus: true},
		{Format: formatJSON, NoStatus: true},
	} {
		if _, err := NewReporter(opts); err == nil {
			t.Fatalf("expected validation error for %+v", opts)
		}
	}
}

func TestNewReporter_DesktopForcesEnvelope(t *testing.T) {
	t.Setenv("DEVSY_UI", "true")
	var events []Event
	reporter, err := NewReporter(ReporterOptions{
		Format: formatPlain, NoStatus: true,
		Envelope: func(event Event) error {
			events = append(events, event)
			return nil
		},
	})
	require.NoError(t, err)
	reporter.Report(Event{Phase: PhaseReady, State: StateStarted})
	require.Len(t, events, 1)
	require.Equal(t, PhaseReady, events[0].Phase)
	require.Equal(t, StateStarted, events[0].State)
	for _, opts := range []ReporterOptions{
		{Format: "ansi", NoStatus: true},
		{Format: formatJSON, NoStatus: true},
		{Format: formatPlain, NoStatus: true},
	} {
		_, err := NewReporter(opts)
		require.Error(t, err)
	}
}

func TestNewCLIReporter_NoStatusPreservesDesktopEnvelopes(t *testing.T) {
	t.Setenv("DEVSY_UI", "true")
	for _, format := range []string{formatAuto, formatJSON, formatPlain, testTextFormat} {
		t.Run(format, func(t *testing.T) {
			var events []Event
			reporter, err := NewCLIReporter(CLIOptions{
				Format: format, NoStatus: true,
				Envelope: func(event Event) error {
					events = append(events, event)
					return nil
				},
			})
			require.NoError(t, err)
			err = Run(
				context.Background(),
				reporter,
				Operation{Phase: PhaseInstallingProvider},
				func(context.Context) error { return nil },
			)
			require.NoError(t, err)
			require.Len(t, events, 2)
			require.Equal(t, PhaseInstallingProvider, events[0].Phase)
			require.Equal(t, StateStarted, events[0].State)
			require.Equal(t, PhaseInstallingProvider, events[1].Phase)
			require.Equal(t, StateSucceeded, events[1].State)
		})
	}
}

func TestNoStatusTeePreservesRunEvents(t *testing.T) {
	t.Setenv("DEVSY_UI", "")
	var out bytes.Buffer
	presentation, err := NewCLIReporter(CLIOptions{Format: formatPlain, Out: &out, NoStatus: true})
	if err != nil {
		t.Fatal(err)
	}
	memory := NewMemoryReporter()
	reporter := Tee(presentation, memory)
	wantErr := errors.New("failure")
	calls := 0
	err = Run(
		context.Background(),
		reporter,
		Operation{Phase: PhaseBuildingImage},
		func(ctx context.Context) error {
			return Run(
				ctx,
				reporter,
				Operation{Phase: PhaseStartingContainer},
				func(context.Context) error {
					calls++
					return wantErr
				},
			)
		},
	)
	require.ErrorIs(t, err, wantErr)
	require.Equal(t, 1, calls)
	require.Empty(t, out.String())
	events := memory.Events()
	require.Len(t, events, 4)
	outer, inner, innerDone, outerDone := events[0], events[1], events[2], events[3]
	require.Equal(t, StateStarted, outer.State)
	require.Equal(t, StateStarted, inner.State)
	require.Equal(t, StateFailed, innerDone.State)
	require.Equal(t, StateFailed, outerDone.State)
	require.NotEmpty(t, outer.OperationID)
	require.NotEmpty(t, inner.OperationID)
	require.NotEqual(t, outer.OperationID, inner.OperationID)
	require.Equal(t, outer.OperationID, inner.ParentOperationID)
	require.Equal(t, outer.OperationID, innerDone.ParentOperationID)
	require.Equal(t, inner.OperationID, innerDone.OperationID)
	require.Equal(t, outer.OperationID, outerDone.OperationID)
	require.Equal(t, outer.ParentOperationID, outerDone.ParentOperationID)
}

func TestNewReporter_RejectsUnknownFormat(t *testing.T) {
	const secret = "private-format-secret"
	for _, noStatus := range []bool{false, true} {
		_, err := NewReporter(
			ReporterOptions{Format: secret, Out: &bytes.Buffer{}, NoStatus: noStatus},
		)
		require.Error(t, err)
		require.NotContains(t, err.Error(), secret)
		require.Contains(t, err.Error(), "choose json, plain, or auto")
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
