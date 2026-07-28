package config

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/status"
)

// Kind discriminates the NDJSON lines `up --result-format json` writes to
// stdout: a stream of "status" lines as phases complete, terminated by
// exactly one "result" or "error" line.
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
	Kind    string `json:"kind"`
	Outcome string `json:"outcome"`
	Message string `json:"message"`
}

// StatusEnvelope is one NDJSON line reporting a phase transition of the up
// pipeline as it happens, ahead of the terminal ResultEnvelope/ErrorEnvelope.
type StatusEnvelope struct {
	Kind    string `json:"kind"`
	Phase   string `json:"phase"`
	Step    string `json:"step,omitempty"`
	Started bool   `json:"started"`
	Error   string `json:"error,omitempty"`
}

// TaskEnvelope is the single line `up --detach` writes to stdout: the ID of
// the background task it submitted, so the caller can poll it later (e.g.
// `workspace task <id>`) instead of waiting for it to finish.
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

// WriteResultJSON serializes env as a success envelope to w. The caller
// supplies the envelope fields; this function stamps Outcome="success" and
// appends a trailing newline.
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

func WriteErrorJSON(w io.Writer, msg string) error {
	env := ErrorEnvelope{
		Kind:    KindError,
		Outcome: "error",
		Message: msg,
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}

// ParseStatusLine parses line as a status NDJSON envelope (see
// WriteStatusJSON), returning ok=false for anything else — plain log text,
// a zap record, or a terminal result/error envelope.
func ParseStatusLine(line string) (status.Event, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") {
		return status.Event{}, false
	}
	var env StatusEnvelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil || env.Kind != KindStatus {
		return status.Event{}, false
	}
	return status.Event{
		Phase:   status.Phase(env.Phase),
		Step:    env.Step,
		Started: env.Started,
		Err:     env.Error,
	}, true
}

// WriteStatusJSON serializes a status.Event as an NDJSON status line to w.
func WriteStatusJSON(w io.Writer, e status.Event) error {
	env := StatusEnvelope{
		Kind:    KindStatus,
		Phase:   string(e.Phase),
		Step:    e.Step,
		Started: e.Started,
		Error:   e.Err,
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}
