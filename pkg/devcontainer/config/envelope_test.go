package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/status"
)

const envelopeTestSecret = "status-secret"

func TestWriteResultJSON(t *testing.T) {
	tests := []struct {
		name        string
		containerID string
		user        string
		workdir     string
		url         string
		warnings    []string
	}{
		{
			name:        "typical values",
			containerID: "abc123def456",
			user:        "vscode",
			workdir:     "/workspaces/project",
			warnings:    nil,
		},
		{
			name:        "root user",
			containerID: "sha256:abcdef1234567890",
			user:        "root",
			workdir:     "/workspaces/my-app",
			warnings:    nil,
		},
		{
			name:        "empty strings",
			containerID: "",
			user:        "",
			workdir:     "",
			warnings:    nil,
		},
		{
			name:        "special characters in container ID",
			containerID: "container:with/special-chars_123",
			user:        "dev-user",
			workdir:     "/workspaces/my project",
			warnings:    nil,
		},
		{
			name:        "with warnings",
			containerID: "abc123",
			user:        "vscode",
			workdir:     "/workspaces/project",
			warnings: []string{
				"cpus: required 128, available 4",
				"memory: required 256gb (274877906944 bytes), available 17179869184 bytes",
			},
		},
		{
			name:        "with url",
			containerID: "abc123",
			user:        "vscode",
			workdir:     "/workspaces/project",
			url:         "http://localhost:8080/?folder=/workspaces/project",
			warnings:    nil,
		},
		{
			name:        "empty url omitted",
			containerID: "abc123",
			user:        "vscode",
			workdir:     "/workspaces/project",
			url:         "",
			warnings:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := WriteResultJSON(&buf, ResultEnvelope{
				ContainerID:           tt.containerID,
				RemoteUser:            tt.user,
				RemoteWorkspaceFolder: tt.workdir,
				URL:                   tt.url,
				Warnings:              tt.warnings,
			})
			if err != nil {
				t.Fatalf("WriteResultJSON returned error: %v", err)
			}

			output := buf.Bytes()
			if output[len(output)-1] != '\n' {
				t.Fatal("output must be newline-terminated")
			}

			var envelope ResultEnvelope
			if err := json.Unmarshal(output, &envelope); err != nil {
				t.Fatalf("output is not valid JSON: %v", err)
			}

			if envelope.Outcome != "success" {
				t.Errorf("outcome = %q, want %q", envelope.Outcome, "success")
			}
			if envelope.ContainerID != tt.containerID {
				t.Errorf("containerId = %q, want %q", envelope.ContainerID, tt.containerID)
			}
			if envelope.RemoteUser != tt.user {
				t.Errorf("remoteUser = %q, want %q", envelope.RemoteUser, tt.user)
			}
			if envelope.RemoteWorkspaceFolder != tt.workdir {
				t.Errorf("remoteWorkspaceFolder = %q, want %q",
					envelope.RemoteWorkspaceFolder, tt.workdir)
			}
			if envelope.URL != tt.url {
				t.Errorf("url = %q, want %q", envelope.URL, tt.url)
			}
			if tt.url == "" && bytes.Contains(output, []byte(`"url"`)) {
				t.Error("url field must be omitted when empty")
			}
			if tt.url != "" && !bytes.Contains(output, []byte(`"url"`)) {
				t.Error("url field must be present when set")
			}
			if len(tt.warnings) == 0 {
				if envelope.Warnings != nil {
					t.Errorf("warnings = %v, want nil (omitempty)", envelope.Warnings)
				}
				if bytes.Contains(output, []byte(`"warnings"`)) {
					t.Error("warnings field must be omitted when empty")
				}
			}
			if len(tt.warnings) > 0 {
				if len(envelope.Warnings) != len(tt.warnings) {
					t.Fatalf(
						"warnings length = %d, want %d",
						len(envelope.Warnings), len(tt.warnings),
					)
				}
				for i, w := range tt.warnings {
					if envelope.Warnings[i] != w {
						t.Errorf("warnings[%d] = %q, want %q", i, envelope.Warnings[i], w)
					}
				}
			}

			if bytes.Contains(output[:len(output)-1], []byte("\n")) {
				t.Error("JSON must be single-line (no embedded newlines)")
			}
		})
	}
}

func TestWriteCLIErrorJSONMessages(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{
			name:    "simple error",
			message: "container failed to start",
		},
		{
			name:    "empty message",
			message: "",
		},
		{
			name:    "message with quotes",
			message: `failed to parse "devcontainer.json": unexpected token`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := WriteCLIErrorJSON(&buf, errors.New(tt.message))
			if err != nil {
				t.Fatalf("WriteCLIErrorJSON returned error: %v", err)
			}

			output := buf.Bytes()
			if output[len(output)-1] != '\n' {
				t.Fatal("output must be newline-terminated")
			}

			var envelope ErrorEnvelope
			if err := json.Unmarshal(output, &envelope); err != nil {
				t.Fatalf("output is not valid JSON: %v", err)
			}

			if envelope.Outcome != "error" {
				t.Errorf("outcome = %q, want %q", envelope.Outcome, "error")
			}
			if envelope.Code != string(clierr.CodeUnknown) {
				t.Errorf("code = %q, want %q", envelope.Code, clierr.CodeUnknown)
			}
			if envelope.Message != tt.message {
				t.Errorf("message = %q, want %q", envelope.Message, tt.message)
			}

			if bytes.Contains(output[:len(output)-1], []byte("\n")) {
				t.Error("JSON must be single-line (no embedded newlines)")
			}
		})
	}
}

func TestWriteCLIErrorJSONIncludesStructuredFields(t *testing.T) {
	var buf bytes.Buffer
	err := &clierr.CLIError{
		Code:    clierr.CodeUnknown,
		Message: "Docker is unavailable",
		Hint:    "Start Docker and retry",
		Context: map[string]string{"context": "desktop-linux"},
	}
	if writeErr := WriteCLIErrorJSON(&buf, err); writeErr != nil {
		t.Fatalf("WriteCLIErrorJSON: %v", writeErr)
	}
	var got ErrorEnvelope
	if unmarshalErr := json.Unmarshal(buf.Bytes(), &got); unmarshalErr != nil {
		t.Fatalf("unmarshal: %v", unmarshalErr)
	}
	if got.Code != "UNKNOWN" || got.Hint == "" || got.Context["context"] != "desktop-linux" {
		t.Fatalf("unexpected structured envelope: %+v", got)
	}
}

func TestWriteStatusJSONRedactsStructuredErrorContext(t *testing.T) {
	t.Setenv("DEVTestStatusSecret", envelopeTestSecret)
	var buf bytes.Buffer
	err := WriteStatusJSON(&buf, status.Event{
		Phase: status.PhaseReady,
		State: status.StateFailed,
		Error: &status.ErrorInfo{
			Code:    envelopeTestSecret,
			Message: "failed with " + envelopeTestSecret,
			Hint:    "retry with " + envelopeTestSecret,
			Context: map[string]string{"token": envelopeTestSecret},
		},
	})
	if err != nil {
		t.Fatalf("WriteStatusJSON returned error: %v", err)
	}
	if strings.Contains(buf.String(), "status-secret") {
		t.Fatalf("status output leaked secret: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "***") {
		t.Fatalf("status output = %s, want redaction marker", buf.String())
	}
}

func TestWriteStatusJSONRedactsMetadata(t *testing.T) {
	t.Setenv("DEVTestStatusSecret", envelopeTestSecret)
	var buf bytes.Buffer
	if err := WriteStatusJSON(&buf, status.Event{
		Pipeline:          "status-secret",
		OperationID:       "op-status-secret",
		ParentOperationID: "parent-status-secret",
		Phase:             status.PhaseReady,
		Step:              "working on status-secret",
		State:             status.StateStarted,
	}); err != nil {
		t.Fatalf("WriteStatusJSON returned error: %v", err)
	}
	if strings.Contains(buf.String(), "status-secret") {
		t.Fatalf("status metadata leaked secret: %s", buf.String())
	}
}

func TestWriteCLIErrorJSONRedactsEnvironmentSecrets(t *testing.T) {
	t.Setenv("DEVSY_TEST_TOKEN", "super-secret-token")
	var buf bytes.Buffer
	err := &clierr.CLIError{
		Code:    clierr.CodeUnknown,
		Message: "request failed with super-secret-token",
		Hint:    "retry with super-secret-token",
		Context: map[string]string{"token": "super-secret-token"},
	}
	if writeErr := WriteCLIErrorJSON(&buf, err); writeErr != nil {
		t.Fatalf("WriteCLIErrorJSON: %v", writeErr)
	}
	if bytes.Contains(buf.Bytes(), []byte("super-secret-token")) {
		t.Fatalf("secret escaped JSON error envelope: %s", buf.Bytes())
	}
}

func TestWriteResultJSON_Recovery(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteResultJSON(&buf, ResultEnvelope{Recovery: true}); err != nil {
		t.Fatalf("WriteResultJSON: %v", err)
	}

	var envelope ResultEnvelope
	if err := json.Unmarshal(buf.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !envelope.Recovery {
		t.Error("recovery field did not round-trip")
	}
}

func TestWriteStatusJSONRoundTrips(t *testing.T) {
	tests := []struct {
		name string
		e    status.Event
	}{
		{name: "entering phase", e: status.Event{Phase: status.PhaseBuildingImage, State: status.StateStarted}},
		{
			name: "completed phase",
			e:    status.Event{Phase: status.PhaseBuildingImage, State: status.StateSucceeded},
		},
		{
			name: "with step",
			e: status.Event{
				Phase: status.PhaseRunningLifecycleHook,
				Step:  "postCreate",
				State: status.StateStarted,
			},
		},
		{
			name: "failed phase",
			e: status.Event{
				Phase: status.PhaseFailed, Step: "building_image", State: status.StateFailed,
				Error: &status.ErrorInfo{Message: "build failed"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteStatusJSON(&buf, tt.e); err != nil {
				t.Fatalf("WriteStatusJSON: %v", err)
			}
			got, ok := ParseStatusLine(buf.String())
			if !ok {
				t.Fatalf("ParseStatusLine did not accept our own output: %q", buf.String())
			}
			if !reflect.DeepEqual(got, tt.e) {
				t.Errorf("round-tripped event = %+v, want %+v", got, tt.e)
			}
		})
	}
}

func TestWriteStatusJSONRejectsNegativeDuration(t *testing.T) {
	err := WriteStatusJSON(&bytes.Buffer{}, status.Event{
		Phase:    status.PhaseReady,
		State:    status.StateSucceeded,
		Duration: -time.Millisecond,
	})
	if err == nil {
		t.Fatal("WriteStatusJSON accepted a negative duration")
	}
}

func TestParseStatusLineRejectsIncompleteEnvelopes(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{name: "not JSON", line: "Pulling image..."},
		{name: "wrong kind", line: `{"kind":"result","schemaVersion":1,"phase":"ready","state":"started"}`},
		{name: "missing schema version", line: `{"kind":"status","phase":"ready","state":"started"}`},
		{name: "missing phase", line: `{"kind":"status","schemaVersion":1,"state":"started"}`},
		{name: "empty phase", line: `{"kind":"status","schemaVersion":1,"phase":"","state":"started"}`},
		{name: "missing state", line: `{"kind":"status","schemaVersion":1,"phase":"ready"}`},
		{name: "legacy started field", line: `{"kind":"status","schemaVersion":1,"phase":"ready","started":true}`},
		{name: "legacy structured error field", line: `{"kind":"status","schemaVersion":1,"phase":"ready","state":"failed","errorInfo":{"message":"boom"}}`},        //nolint:lll // exact compatibility fixture
		{name: "legacy string error field", line: `{"kind":"status","schemaVersion":1,"phase":"ready","state":"failed","error":"boom"}`},                            //nolint:lll // exact compatibility fixture
		{name: "error without message", line: `{"kind":"status","schemaVersion":1,"phase":"ready","state":"failed","error":{"code":"boom"}}`},                       //nolint:lll // exact validation fixture
		{name: "error with unknown field", line: `{"kind":"status","schemaVersion":1,"phase":"ready","state":"failed","error":{"message":"boom","details":"old"}}`}, //nolint:lll // exact validation fixture
		{name: "failed without structured error", line: `{"kind":"status","schemaVersion":1,"phase":"ready","state":"failed"}`},                                     //nolint:lll // exact validation fixture
		{name: "unknown state", line: `{"kind":"status","schemaVersion":1,"phase":"ready","state":"running"}`},
		{name: "negative duration", line: `{"kind":"status","schemaVersion":1,"phase":"ready","state":"succeeded","durationMs":-1}`},            //nolint:lll // exact validation fixture
		{name: "duration overflow", line: `{"kind":"status","schemaVersion":1,"phase":"ready","state":"succeeded","durationMs":9223372036855}`}, //nolint:lll // exact validation fixture
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := ParseStatusLine(tt.line); ok {
				t.Errorf("ParseStatusLine(%q) = ok, want rejected", tt.line)
			}
		})
	}
}

func TestParseStatusLineAcceptsSucceededState(t *testing.T) {
	event, ok := ParseStatusLine(`{"kind":"status","schemaVersion":1,"phase":"ready","state":"succeeded"}`)
	if !ok {
		t.Fatal("ParseStatusLine rejected a valid completed-phase envelope")
	}
	if event.State != status.StateSucceeded {
		t.Errorf("State = %q, want succeeded", event.State)
	}
}

func TestStatusLineRoundTripsPipeline(t *testing.T) {
	var buf bytes.Buffer
	want := status.Event{
		Pipeline: status.PipelineProvider,
		Phase:    status.Phase("downloading_binaries"),
		Step:     "docker",
		State:    status.StateStarted,
	}
	if err := WriteStatusJSON(&buf, want); err != nil {
		t.Fatalf("WriteStatusJSON: %v", err)
	}

	got, ok := ParseStatusLine(buf.String())
	if !ok {
		t.Fatalf("ParseStatusLine did not recognize %q", buf.String())
	}
	if got != want {
		t.Errorf("round-trip: got %+v, want %+v", got, want)
	}
}

func TestParseStatusLineWithoutPipeline(t *testing.T) {
	line := `{"kind":"status","schemaVersion":1,"phase":"ready","state":"succeeded"}`

	got, ok := ParseStatusLine(line)
	if !ok {
		t.Fatalf("ParseStatusLine(%q) = not ok", line)
	}
	if got.Pipeline != "" {
		t.Errorf("pipeline = %q, want empty", got.Pipeline)
	}
	if got.Phase != status.PhaseReady {
		t.Errorf("phase = %q, want %q", got.Phase, status.PhaseReady)
	}
}

func TestParseStatusLineAcceptsVersionedLifecycleEvent(t *testing.T) {
	line := `{"kind":"status","schemaVersion":1,"pipeline":"workspace_up","operationId":"op-17","phase":"building_image","state":"succeeded","durationMs":8214}` //nolint:lll // exact protocol fixture
	event, ok := ParseStatusLine(line)
	if !ok {
		t.Fatal("ParseStatusLine rejected a versioned lifecycle event")
	}
	if event.State != status.StateSucceeded || event.OperationID != "op-17" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if event.Duration != 8214*time.Millisecond {
		t.Errorf("duration = %v, want %v", event.Duration, 8214*time.Millisecond)
	}
}

func TestWriteStatusJSONIncludesCurrentLifecycleFields(t *testing.T) {
	var buf bytes.Buffer
	want := status.Event{
		Pipeline:          status.PipelineWorkspaceUp,
		OperationID:       "op-17",
		ParentOperationID: "op-1",
		Phase:             status.PhaseBuildingImage,
		State:             status.StateFailed,
		Duration:          1500 * time.Millisecond,
		Error:             &status.ErrorInfo{Code: "docker_daemon_unreachable", Message: "daemon unavailable", Hint: "Start Docker and retry."},
	}
	if err := WriteStatusJSON(&buf, want); err != nil {
		t.Fatalf("WriteStatusJSON: %v", err)
	}
	var got StatusEnvelope
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.SchemaVersion != 1 || got.State != status.StateFailed || got.DurationMs != 1500 || got.Error == nil || got.Error.Code != "docker_daemon_unreachable" {
		t.Fatalf("unexpected envelope: %+v", got)
	}
	if got.State != status.StateFailed || got.Error == nil {
		t.Fatalf("current status fields missing: %+v", got)
	}
}
