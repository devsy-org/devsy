package config

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/status"
)

const (
	KindStatus = "status"
	KindResult = "result"
	KindError  = "error"
	KindTask   = "task"
)

type ResultEnvelope struct {
	Kind                  string   `json:"kind"`
	Outcome               string   `json:"outcome"`
	ContainerID           string   `json:"containerId"`
	RemoteUser            string   `json:"remoteUser"`
	RemoteWorkspaceFolder string   `json:"remoteWorkspaceFolder"`
	URL                   string   `json:"url,omitempty"`
	Warnings              []string `json:"warnings,omitempty"`
	Recovery              bool     `json:"recovery,omitempty"`
}

type ErrorEnvelope struct {
	Kind    string            `json:"kind"`
	Outcome string            `json:"outcome"`
	Code    string            `json:"code,omitempty"`
	Message string            `json:"message"`
	Hint    string            `json:"hint,omitempty"`
	Context map[string]string `json:"context,omitempty"`
}

// StatusEnvelope is one NDJSON line reporting a phase transition of a
// pipeline. This is the current structured status protocol.
type StatusEnvelope struct {
	Kind              string            `json:"kind"`
	SchemaVersion     int               `json:"schemaVersion"`
	Pipeline          string            `json:"pipeline,omitempty"`
	OperationID       string            `json:"operationId,omitempty"`
	ParentOperationID string            `json:"parentOperationId,omitempty"`
	Phase             string            `json:"phase"`
	Step              string            `json:"step,omitempty"`
	State             status.State      `json:"state"`
	DurationMs        int64             `json:"durationMs,omitempty"`
	Error             *status.ErrorInfo `json:"error,omitempty"`
}

// TaskEnvelope is the single line `up --detach` writes to stdout.
type TaskEnvelope struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// WriteTaskJSON serializes a submitted task's ID as an NDJSON line to w.
func WriteTaskJSON(w io.Writer, id string) error {
	data, err := json.Marshal(TaskEnvelope{Kind: KindTask, ID: id})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// WriteResultJSON serializes env as a success envelope.
func WriteResultJSON(w io.Writer, env ResultEnvelope) error {
	env.Kind = KindResult
	env.Outcome = "success"
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// WriteCLIErrorJSON serializes a normalized CLI error.
func WriteCLIErrorJSON(w io.Writer, err error) error {
	classified := clierr.Classify(err)
	redactor := secrets.NewEnvironmentRedactor(os.Environ())
	env := ErrorEnvelope{
		Kind:    KindError,
		Outcome: "error",
		Code:    string(classified.Code),
		Message: classified.Message,
		Hint:    classified.Hint,
		Context: redactContext(classified.Context, redactor),
	}
	env.Message = redactor.Redact(env.Message)
	env.Hint = redactor.Redact(env.Hint)
	data, marshalErr := json.Marshal(env)
	if marshalErr != nil {
		return marshalErr
	}
	_, writeErr := fmt.Fprintf(w, "%s\n", data)
	return writeErr
}

// ParseStatusLine parses line as a current status NDJSON envelope.
func ParseStatusLine(line string) (status.Event, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") {
		return status.Event{}, false
	}
	var env StatusEnvelope
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &fields); err != nil || !currentStatusFields(fields) {
		return status.Event{}, false
	}
	var errorMessage string
	if raw, ok := fields["error"]; ok && json.Unmarshal(raw, &errorMessage) == nil {
		return status.Event{}, false
	}
	if raw, ok := fields["error"]; ok && !currentStatusError(raw) {
		return status.Event{}, false
	}
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil ||
		env.Kind != KindStatus || env.SchemaVersion != 1 || env.Phase == "" ||
		!validState(env.State) || env.DurationMs < 0 ||
		env.DurationMs > int64((time.Duration(1<<63-1))/time.Millisecond) ||
		(env.State == status.StateFailed && env.Error == nil) {
		return status.Event{}, false
	}
	return status.Event{
		Pipeline:          status.Pipeline(env.Pipeline),
		OperationID:       env.OperationID,
		ParentOperationID: env.ParentOperationID,
		Phase:             status.Phase(env.Phase),
		Step:              env.Step,
		State:             env.State,
		Duration:          time.Duration(env.DurationMs) * time.Millisecond,
		Error:             env.Error,
	}, true
}

func currentStatusFields(fields map[string]json.RawMessage) bool {
	for field := range fields {
		switch field {
		case "kind", "schemaVersion", "pipeline", "operationId", "parentOperationId",
			"phase", "step", "state", "durationMs", "error":
		default:
			return false
		}
	}
	return true
}

func currentStatusError(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return false
	}
	for field := range fields {
		switch field {
		case "code", "message", "hint", "context":
		default:
			return false
		}
	}
	var info status.ErrorInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return false
	}
	return info.Message != ""
}

func validState(state status.State) bool {
	return status.ValidState(state)
}

// WriteStatusJSON serializes a status.Event as an NDJSON status line.
func WriteStatusJSON(w io.Writer, e status.Event) error {
	if e.Phase == "" || !validState(e.State) || e.Duration < 0 ||
		(e.State == status.StateFailed && e.Error == nil) {
		return fmt.Errorf("status event requires a phase and recognized state")
	}
	redactor := secrets.NewEnvironmentRedactor(os.Environ())
	env := StatusEnvelope{
		Kind:              KindStatus,
		SchemaVersion:     1,
		Pipeline:          redactor.Redact(string(e.Pipeline)),
		OperationID:       redactor.Redact(e.OperationID),
		ParentOperationID: redactor.Redact(e.ParentOperationID),
		Phase:             redactor.Redact(string(e.Phase)),
		Step:              redactor.Redact(e.Step),
		State:             e.State,
		DurationMs:        e.Duration.Milliseconds(),
		Error:             redactError(e.Error, redactor),
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

func redactError(info *status.ErrorInfo, redactor *secrets.Redactor) *status.ErrorInfo {
	if info == nil {
		return nil
	}
	return &status.ErrorInfo{
		Code:    redactor.Redact(info.Code),
		Message: redactor.Redact(info.Message),
		Hint:    redactor.Redact(info.Hint),
		Context: redactContext(info.Context, redactor),
	}
}

func redactContext(values map[string]string, redactor *secrets.Redactor) map[string]string {
	if len(values) == 0 {
		return nil
	}
	redacted := make(map[string]string, len(values))
	for key, value := range values {
		redacted[redactor.Redact(key)] = redactor.Redact(value)
	}
	return redacted
}
