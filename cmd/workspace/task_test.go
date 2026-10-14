package workspace

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/task"
	"github.com/stretchr/testify/require"
)

const (
	testResultContainerID = "abc123"
	testBuild             = "build"
	testPhase             = "building_image"
	testOp                = "op-1"
	testStarted           = "started"
	testPlain             = "task task-1: building_image\n"
)

func TestResultEnvelopeFrom_NilResult(t *testing.T) {
	got := resultEnvelopeFrom(&task.State{})
	if got.ContainerID != "" || got.RemoteUser != "" || got.Recovery || len(got.Warnings) != 0 {
		t.Errorf("expected zero envelope, got %+v", got)
	}
}

type taskTransitionCase struct {
	name                     string
	change                   func(*task.State)
	first, jsonSilent        bool
	phase, step, operationID string
	state                    status.State
	duration                 int64
	plain                    string
}

func taskTransitionCases() []taskTransitionCase {
	return []taskTransitionCase{
		{"nil-last", nil, true, false, testPhase, testBuild, testOp, testStarted, 100, testPlain},
		{"identical", nil, false, true, "", "", "", "", 0, ""},
		{
			"duration-only", func(s *task.State) { s.DurationMs = 250 }, false, false,
			testPhase, testBuild, testOp, testStarted, 250, "",
		},
		{
			"phase-only",
			func(s *task.State) { s.Phase = "running_command" },
			false,
			false,
			"running_command",
			testBuild,
			testOp,
			testStarted,
			100,
			"task task-1: running_command\n",
		},
		{
			"step-only", func(s *task.State) { s.Step = "execute" }, false, false,
			testPhase, "execute", testOp, testStarted, 100, testPlain,
		},
		{
			"status-only", func(s *task.State) { s.Status = task.StatusSucceeded }, false, false,
			testPhase, testBuild, testOp, "succeeded", 100, testPlain,
		},
		{
			"operationID-only", func(s *task.State) { s.OperationID = "op-2" }, false, false,
			testPhase, testBuild, "op-2", testStarted, 100, testPlain,
		},
		{"empty-phase", func(s *task.State) { s.Phase = "" }, false, true, "", "", "", "", 0, ""},
	}
}

func TestEmitTaskTransition(t *testing.T) {
	for _, mode := range []struct {
		name string
		json bool
	}{{"JSON", true}, {"plain", false}} {
		t.Run(mode.name, func(t *testing.T) {
			for _, tc := range taskTransitionCases() {
				t.Run(tc.name, func(t *testing.T) {
					last, current := taskTransitionFixture(), taskTransitionFixture()
					if tc.first {
						last = nil
					}
					if tc.change != nil {
						tc.change(current)
					}
					output := captureTaskTransitionStdout(
						t,
						func() { emitTaskTransition(last, current, mode.json) },
					)
					switch {
					case !mode.json:
						require.Equal(t, tc.plain, output)
					case tc.jsonSilent:
						require.Equal(t, "", output)
					default:
						checkTaskTransitionJSON(t, output, config.StatusEnvelope{
							Kind: "status", SchemaVersion: 1, ParentOperationID: "parent-1",
							Phase: tc.phase, Step: tc.step, OperationID: tc.operationID,
							State: tc.state, DurationMs: tc.duration,
						})
					}
				})
			}
		})
	}
}

func taskTransitionFixture() *task.State {
	return &task.State{
		ID:                "task-1",
		Phase:             testPhase,
		Step:              testBuild,
		Status:            task.StatusRunning,
		OperationID:       testOp,
		ParentOperationID: "parent-1",
		DurationMs:        100,
	}
}

func captureTaskTransitionStdout(t *testing.T, emit func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	original := os.Stdout
	restore := func() {
		if os.Stdout == writer {
			os.Stdout = original
		}
	}
	readerClosed, writerClosed := false, false
	defer func() {
		restore()
		if !writerClosed {
			_ = writer.Close()
		}
		if !readerClosed {
			_ = reader.Close()
		}
	}()
	os.Stdout = writer
	func() {
		defer restore()
		emit()
	}()
	writeCloseErr := writer.Close()
	writerClosed = true
	output, readErr := io.ReadAll(reader)
	readCloseErr := reader.Close()
	readerClosed = true
	require.NoError(t, writeCloseErr)
	require.NoError(t, readErr)
	require.NoError(t, readCloseErr)
	return string(output)
}

func checkTaskTransitionJSON(t *testing.T, output string, expected config.StatusEnvelope) {
	t.Helper()
	require.NotEmpty(t, output)
	require.Equal(t, byte('\n'), output[len(output)-1])
	require.Equal(t, 1, strings.Count(output, "\n"))
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(output), &fields))
	require.Len(t, fields, 8)
	for _, key := range []string{
		"kind", "schemaVersion", "operationId", "parentOperationId",
		"phase", "step", "state", "durationMs",
	} {
		require.Contains(t, fields, key)
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	var got config.StatusEnvelope
	require.NoError(t, decoder.Decode(&got))
	require.Equal(t, expected, got)
	var extra any
	require.ErrorIs(t, decoder.Decode(&extra), io.EOF)
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

func TestTaskStatusEventPreservesTerminalStructuredState(t *testing.T) {
	event := taskStatusEvent(&task.State{
		Phase:             string(status.PhaseBuildingImage),
		Step:              "image",
		Status:            task.StatusFailed,
		OperationID:       "op-17",
		ParentOperationID: "op-9",
		DurationMs:        1200,
		Error:             "Docker is unavailable.",
		ErrorCode:         "docker_daemon_unreachable",
		ErrorHint:         "Start Docker and retry.",
		ErrorContext:      map[string]string{"context": "desktop-linux"},
	})
	if event.State != status.StateFailed || event.OperationID != "op-17" ||
		event.Duration != 1200*time.Millisecond {
		t.Fatalf("unexpected task status event: %+v", event)
	}
	if event.Error == nil || event.Error.Code != "docker_daemon_unreachable" ||
		event.Error.Context["context"] != "desktop-linux" {
		t.Fatalf("structured error was lost: %+v", event.Error)
	}
}
