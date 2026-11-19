package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/status"
)

const (
	outerOperation = "outer"
	innerOperation = "inner"
	phaseContainer = "container"
)

func TestSameDepthDifferentFailuresOmitPhase(t *testing.T) {
	m := NewMetricsReporter()
	cause := errors.New("shared failure")
	m.Report(status.Event{
		OperationID: "build", Phase: status.PhaseBuildingImage,
		State: status.StateFailed, Duration: time.Second, Cause: cause,
	})
	m.Report(status.Event{
		OperationID: phaseContainer, Phase: status.PhaseStartingContainer,
		State: status.StateFailed, Duration: 2 * time.Second, Cause: cause,
	})
	snapshot := m.Finish(cause)
	if snapshot.FailedPhase != "" || snapshot.PhaseDurationsMs[phaseBuild] != 1000 ||
		snapshot.PhaseDurationsMs[phaseContainer] != 2000 {
		t.Fatalf("ambiguous failure or durations incorrect: %+v", snapshot)
	}
}

func TestMetricsCausalityAndImmutability(t *testing.T) {
	m := NewMetricsReporter()
	cause := errors.New("secret /private/path")
	m.Report(
		status.Event{
			OperationID: outerOperation,
			Phase:       status.PhaseBuildingImage,
			State:       status.StateStarted,
		},
	)
	m.Report(status.Event{
		OperationID: innerOperation, ParentOperationID: outerOperation,
		Phase: status.PhaseStartingContainer, State: status.StateStarted,
	})
	m.Report(status.Event{
		OperationID:       innerOperation,
		ParentOperationID: outerOperation,
		Phase:             status.PhaseStartingContainer,
		State:             status.StateFailed,
		Duration:          2 * time.Second,
		Cause:             cause,
	})
	m.Report(status.Event{
		OperationID: innerOperation, Phase: status.PhaseStartingContainer,
		State: status.StateFailed, Duration: time.Hour, Cause: cause,
	})
	m.Report(status.Event{
		OperationID: outerOperation, Phase: status.PhaseBuildingImage,
		State: status.StateFailed, Duration: 3 * time.Second, Cause: cause,
	})
	ctx := WithMetrics(context.Background(), m)
	RecordProviderOperation(ctx, ProviderOperation{
		ProviderType: ProviderTypeExternalSDK, Operation: operationRunImage, Duration: time.Second,
	})
	s := m.Finish(cause)
	if s.FailedPhase != phaseContainer || s.PhaseDurationsMs[phaseContainer] != 2000 ||
		s.PhaseDurationsMs[phaseBuild] != 3000 {
		t.Fatalf("wrong metrics: %+v", s)
	}
	if len(s.ProviderOperations) != 1 || s.ProviderOperations[0].Operation != operationRunImage {
		t.Fatalf("missing provider metric: %+v", s)
	}
	assertMetricsSnapshotPrivacyAndImmutability(t, m, s)
}

func assertMetricsSnapshotPrivacyAndImmutability(
	t *testing.T,
	m *MetricsReporter,
	s MetricsSnapshot,
) {
	t.Helper()
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private") || strings.Contains(string(encoded), "secret") {
		t.Fatal("cause leaked")
	}
	s.PhaseDurationsMs[phaseContainer] = 0
	s.ProviderOperations[0].Count = 0
	m.Report(
		status.Event{OperationID: "late", Phase: status.PhaseReady, State: status.StateSucceeded},
	)
	again := m.Finish(nil)
	if again.FailedPhase != phaseContainer || again.PhaseDurationsMs[phaseContainer] != 2000 ||
		again.ProviderOperations[0].Count != 1 {
		t.Fatalf("snapshot mutated: %+v", again)
	}
}

func TestRecoveredAndPanicFailuresAreNotAttributed(t *testing.T) {
	m := NewMetricsReporter()
	cause := errors.New("private panic")
	m.Report(
		status.Event{
			OperationID: "recovered",
			Phase:       status.PhaseBuildingImage,
			State:       status.StateFailed,
			Cause:       cause,
		},
	)
	s := m.Finish(clierr.NewPanic("private panic"))
	if s.FailedPhase != "" || s.ErrorCode != "PANIC" {
		t.Fatalf("invented attribution: %+v", s)
	}
}

func TestIdenticalRecoveredAndUncaughtPanicDoesNotInventPhase(t *testing.T) {
	m := NewMetricsReporter()
	ctx := WithMetrics(context.Background(), m)
	var finalErr error
	func() {
		defer func() { finalErr = clierr.NewPanic(recover()) }()
		func() {
			defer func() { _ = recover() }()
			_ = status.Run(
				ctx,
				status.Nop(),
				status.Operation{Phase: status.PhaseBuildingImage},
				func(context.Context) error { panic("private primitive") },
			)
		}()
		panic("private primitive")
	}()
	snapshot := m.Finish(finalErr)
	if snapshot.ErrorCode != "PANIC" || snapshot.FailedPhase != "" {
		t.Fatalf("invented panic attribution: %+v", snapshot)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || strings.Contains(string(encoded), "private primitive") {
		t.Fatalf("panic leaked into snapshot: %s %v", encoded, err)
	}
}

func TestExportMetricsAppendAndRejectUnsafe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metrics.jsonl")
	t.Setenv("DEVSY_METRICS_FILE", path)
	if err := ExportMetrics(MetricsSnapshot{DurationMs: 1, ProviderOperations: []ProviderMetric{
		{
			ProviderType:     ProviderTypeExternalSDK,
			Operation:        operationRunImage,
			Count:            1,
			DurationMs:       4,
			RuntimeErrorCode: "timeout",
		},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := ExportMetrics(MetricsSnapshot{DurationMs: 2}); err != nil {
		t.Fatal(err)
	}
	assertExportRecordsAndSymlink(t, dir, path)
}

func assertExportRecordsAndSymlink(t *testing.T, dir, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected two records: %q", data)
	}
	assertExportRecordValues(t, lines, data)
	assertExportSymlinkRejected(t, dir, path)
}

func assertExportRecordValues(t *testing.T, lines []string, data []byte) {
	t.Helper()
	var first, second MetricsSnapshot
	if json.Unmarshal([]byte(lines[0]), &first) != nil ||
		json.Unmarshal([]byte(lines[1]), &second) != nil ||
		first.DurationMs != 1 ||
		second.DurationMs != 2 ||
		len(first.ProviderOperations) != 1 ||
		first.ProviderOperations[0].RuntimeErrorCode != "timeout" {
		t.Fatalf("bad records: %q", data)
	}
}

func assertExportSymlinkRejected(t *testing.T, dir, path string) {
	t.Helper()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(path, link); err == nil {
		t.Setenv("DEVSY_METRICS_FILE", link)
		if ExportMetrics(MetricsSnapshot{}) == nil {
			t.Fatal("accepted symlink")
		}
	}
}
