package telemetry

import (
	"errors"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/status"
)

const phaseBuild = "build"

func TestSameCategoryNestedDurationAndRetries(t *testing.T) {
	m := NewMetricsReporter()
	m.Report(
		status.Event{
			OperationID: "a",
			Phase:       status.PhaseBuildingImage,
			State:       status.StateStarted,
		},
	)
	m.Report(status.Event{
		OperationID: "b", ParentOperationID: "a", Phase: status.PhaseBuildingImage,
		State: status.StateSucceeded, Duration: time.Second,
	})
	m.Report(status.Event{
		OperationID: "a",
		Phase:       status.PhaseBuildingImage,
		State:       status.StateSucceeded,
		Duration:    3 * time.Second,
	})
	m.Report(status.Event{
		OperationID: "c",
		Phase:       status.PhaseBuildingImage,
		State:       status.StateSucceeded,
		Duration:    2 * time.Second,
	})
	result := m.Finish(nil)
	if result.PhaseDurationsMs[phaseBuild] != 5000 || result.FailedPhase != "" {
		t.Fatalf("bad durations: %+v", result)
	}
}

func TestRecoveredChildCannotClaimReusedError(t *testing.T) {
	m := NewMetricsReporter()
	cause := errors.New("reused")
	m.Report(
		status.Event{
			OperationID: "parent",
			Phase:       status.PhaseBuildingImage,
			State:       status.StateSucceeded,
		},
	)
	m.Report(status.Event{
		OperationID: "recovered", ParentOperationID: "parent", Phase: status.PhaseStartingContainer,
		State: status.StateFailed, Cause: cause,
	})
	m.Report(status.Event{
		OperationID: "actual",
		Phase:       status.PhaseInjectingAgent,
		State:       status.StateFailed,
		Cause:       cause,
	})
	result := m.Finish(cause)
	if result.FailedPhase != "agent" {
		t.Fatalf("recovered failure claimed result: %+v", result)
	}
}

func TestJoinedCleanupDoesNotReplacePrimaryFailure(t *testing.T) {
	m := NewMetricsReporter()
	primary := errors.New("primary")
	cleanup := errors.New("cleanup")
	m.Report(status.Event{
		OperationID: "main",
		Phase:       status.PhaseBuildingImage,
		State:       status.StateFailed,
		Cause:       primary,
	})
	m.Report(status.Event{
		OperationID: "outer-cleanup",
		Phase:       status.PhaseStoppingWorkspace,
		State:       status.StateFailed,
		Cause:       cleanup,
	})
	m.Report(status.Event{
		OperationID: "inner-cleanup", ParentOperationID: "outer-cleanup",
		Phase: status.PhaseDeletingWorkspace, State: status.StateFailed, Cause: cleanup,
	})
	snapshot := m.Finish(errors.Join(primary, cleanup))
	if snapshot.FailedPhase != phaseBuild {
		t.Fatalf("cleanup replaced primary failure: %+v", snapshot)
	}
}
