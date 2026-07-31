package workspace

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/task"
)

const testResultContainerID = "abc123"

func TestResultEnvelopeFrom_NilResult(t *testing.T) {
	got := resultEnvelopeFrom(&task.State{})
	if got.ContainerID != "" || got.RemoteUser != "" || got.Recovery || len(got.Warnings) != 0 {
		t.Errorf("expected zero envelope, got %+v", got)
	}
}

func TestResultEnvelopeFrom_PopulatedResult(t *testing.T) {
	state := &task.State{
		Result: &config.Result{
			HostWarnings:      []string{"warn"},
			RecoveryContainer: true,
			ContainerDetails: &config.ContainerDetails{
				ID: testResultContainerID,
			},
		},
	}

	got := resultEnvelopeFrom(state)
	if got.ContainerID != testResultContainerID {
		t.Errorf("ContainerID = %q, want %q", got.ContainerID, testResultContainerID)
	}
	if !got.Recovery {
		t.Error("expected Recovery = true")
	}
	if len(got.Warnings) != 1 || got.Warnings[0] != "warn" {
		t.Errorf("Warnings = %v", got.Warnings)
	}
}

func TestTaskErrorMessage(t *testing.T) {
	t.Run("uses recorded error", func(t *testing.T) {
		got := taskErrorMessage(&task.State{ID: "t1", Error: "boom"})
		if got != "boom" {
			t.Errorf("got %q, want %q", got, "boom")
		}
	})

	t.Run("falls back when empty", func(t *testing.T) {
		got := taskErrorMessage(&task.State{ID: "t1"})
		if got == "" {
			t.Error("got empty message, want a non-empty fallback")
		}
	})
}
