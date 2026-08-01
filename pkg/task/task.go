// Package task tracks detached background work so its status can
// be polled independently of the CLI invocation that started it.
package task

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/gofrs/flock"
)

var (
	ErrCanceled  = errors.New("canceled")
	ErrAbandoned = errors.New("worker exited without recording a result")
)

// WorkerProcessName returns the background-process name a detached task's
// worker is registered under, matching StartBackground's naming. Shared so
// a caller can locate the worker's captured stdout/stderr via
// pkg/config.PathManager.ProcessStreamsFile without duplicating the scheme.
func WorkerProcessName(id string) string {
	return "devsy-up-" + id
}

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
// state.
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
	// Held for the worker process's lifetime; see HoldWorkerLock.
	workerLock *flock.Flock
}

func (t *Task) ID() string { return t.id }

func (t *Task) SetPID(pid int) error {
	return t.store.update(t.id, func(s *State) {
		s.PID = pid
	})
}

// HoldWorkerLock claims this task's worker lock for the rest of the process's
// life, marking it as actively being worked on. Callers must not release it:
// the kernel does so when the process exits, crashes, or is killed.
//
// Returns an error if another process already holds the lock, since that means
// a worker for this task is already running.
func (t *Task) HoldWorkerLock() error {
	path, err := t.store.workerLockPath(t.id)
	if err != nil {
		return err
	}

	lock := flock.New(path)
	locked, err := lock.TryLock()
	if err != nil {
		return fmt.Errorf("lock task %s worker: %w", t.id, err)
	}
	if !locked {
		return fmt.Errorf("task %s already has a running worker", t.id)
	}
	t.workerLock = lock
	return nil
}

// ReleaseWorkerLockForTest drops the lock HoldWorkerLock acquired, letting a
// test simulate a dead worker. Production code must never call it.
//
// Not in export_test.go: other packages' tests need it, and a _test.go file is
// only compiled into its own package's test binary.
func (t *Task) ReleaseWorkerLockForTest() error {
	if t.workerLock == nil {
		return nil
	}
	lock := t.workerLock
	t.workerLock = nil
	if err := lock.Unlock(); err != nil {
		return fmt.Errorf("unlock task %s worker: %w", t.id, err)
	}
	return nil
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

// Succeed is a no-op once the task is terminal, so a worker that finishes
// concurrently with a Cancel can't overwrite the canceled state with success.
func (t *Task) Succeed(result *config.Result) error {
	return t.store.update(t.id, func(s *State) {
		if s.Status.Terminal() {
			return
		}
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
	if pid == 0 || !t.store.workerAlive(t.id) {
		return nil
	}
	return command.Kill(strconv.Itoa(pid))
}

// Fail preserves an existing terminal state, so the error a canceled worker
// reports on its way out doesn't mask ErrCanceled as the reason it stopped.
func (t *Task) Fail(err error) error {
	return t.store.update(t.id, func(s *State) {
		if s.Status.Terminal() {
			return
		}
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
		// A terminal task is done being described; late events from a worker
		// still unwinding must not resurrect it or rewrite its outcome.
		if s.Status.Terminal() {
			return
		}
		if e.Phase == status.PhaseFailed {
			s.Status = StatusFailed
			s.Phase = string(e.Phase)
			s.Step = e.Step
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
