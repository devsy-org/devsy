// Package task tracks detached background work (e.g. `workspace up
// --detach`) so its status can be polled independently of the CLI
// invocation that started it.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/status"
)

var ErrCanceled = errors.New("canceled")

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
)

func (s Status) Terminal() bool {
	return s == StatusSucceeded || s == StatusFailed
}

// State is the JSON snapshot persisted for a task. PID names the OS process
// doing the work, distinct from whatever process is merely polling this
// state. Command/WorkspaceID are caller-supplied labels for `task list` and
// don't affect behavior.
type State struct {
	ID          string         `json:"id"`
	Command     string         `json:"command,omitempty"`
	WorkspaceID string         `json:"workspaceId,omitempty"`
	Status      Status         `json:"status"`
	Phase       string         `json:"phase,omitempty"`
	Step        string         `json:"step,omitempty"`
	Error       string         `json:"error,omitempty"`
	Result      *config.Result `json:"result,omitempty"`
	PID         int            `json:"pid,omitempty"`
	StartedAt   time.Time      `json:"startedAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
}

// CreateOptions labels a task at creation time for later listing.
type CreateOptions struct {
	Command     string
	WorkspaceID string
}

// Task is a handle to a single background task, bound to the Store it was
// created in. Report may be called from multiple goroutines; each call
// serializes its own read-modify-write of the state file.
type Task struct {
	store *Store
	id    string
}

func (t *Task) ID() string { return t.id }

func (t *Task) SetPID(pid int) error {
	return t.store.update(t.id, func(s *State) {
		s.PID = pid
	})
}

// SetWorkspaceID corrects the task's workspace label to the resolved ID,
// which may differ from whatever label it was created with (e.g. a raw
// source string guessed before workspace resolution ran). client.Status
// looks tasks up by this label, so it must end up accurate.
func (t *Task) SetWorkspaceID(id string) error {
	return t.store.update(t.id, func(s *State) {
		s.WorkspaceID = id
	})
}

func (t *Task) Reporter() status.Reporter {
	return taskReporter{task: t}
}

func (t *Task) Succeed(result *config.Result) error {
	return t.store.update(t.id, func(s *State) {
		s.Status = StatusSucceeded
		s.Result = result
		s.Error = ""
	})
}

// Cancel is safe to call even if the process already exited on its own. The
// terminal check and state transition happen atomically under the same
// lock, so a concurrent report from the task's own worker can't race with
// marking it canceled here.
func (t *Task) Cancel() error {
	var pid int
	err := t.store.update(t.id, func(s *State) {
		if s.Status.Terminal() {
			return
		}
		pid = s.PID
		s.Status = StatusFailed
		s.Error = ErrCanceled.Error()
	})
	if err != nil {
		return err
	}
	if pid == 0 {
		return nil
	}
	return command.Kill(strconv.Itoa(pid))
}

func (t *Task) Fail(err error) error {
	return t.store.update(t.id, func(s *State) {
		s.Status = StatusFailed
		if err != nil {
			s.Error = err.Error()
		}
	})
}

type taskReporter struct {
	task *Task
}

func (r taskReporter) Report(e status.Event) {
	_ = r.task.store.update(r.task.id, func(s *State) {
		if e.Phase == status.PhaseFailed {
			s.Error = e.Err
			return
		}
		if s.Status == StatusPending {
			s.Status = StatusRunning
		}
		s.Phase = string(e.Phase)
		s.Step = e.Step
	})
}

func (s *State) touch() {
	s.UpdatedAt = time.Now()
}

func marshalState(s *State) ([]byte, error) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal task state: %w", err)
	}
	return data, nil
}
