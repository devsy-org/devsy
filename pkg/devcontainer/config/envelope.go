package config

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

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
	Kind    string `json:"kind"`
	Outcome string `json:"outcome"`
	Message string `json:"message"`
}

// StatusEnvelope is one NDJSON line reporting a phase transition of the up
// pipeline. Started is a pointer so an omitted field is distinguishable
// from an explicit false.
type StatusEnvelope struct {
	Kind     string `json:"kind"`
	Pipeline string `json:"pipeline,omitempty"`
	Phase    string `json:"phase"`
	Step     string `json:"step,omitempty"`
	Started  *bool  `json:"started"`
	Error    string `json:"error,omitempty"`
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

// ParseStatusLine parses line as a status NDJSON envelope, rejecting lines
// missing phase or started so an incidental non-status line isn't mistaken
// for one.
func ParseStatusLine(line string) (status.Event, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "{") {
		return status.Event{}, false
	}
	var env StatusEnvelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil ||
		env.Kind != KindStatus || env.Phase == "" || env.Started == nil {
		return status.Event{}, false
	}
	return status.Event{
		Pipeline: status.Pipeline(env.Pipeline),
		Phase:    status.Phase(env.Phase),
		Step:     env.Step,
		Started:  *env.Started,
		Err:      env.Error,
	}, true
}

// WriteStatusJSON serializes a status.Event as an NDJSON status line.
func WriteStatusJSON(w io.Writer, e status.Event) error {
	started := e.Started
	env := StatusEnvelope{
		Kind:     KindStatus,
		Pipeline: string(e.Pipeline),
		Phase:    string(e.Phase),
		Step:     e.Step,
		Started:  &started,
		Error:    e.Err,
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", data)
	return err
}
