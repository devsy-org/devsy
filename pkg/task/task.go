// Package task tracks detached background work so its status can
// be polled independently of the CLI invocation that started it.
package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/gofrs/flock"
)

var (
	ErrCanceled  = errors.New("canceled")
	ErrAbandoned = errors.New("worker exited without recording a result")
)

// WorkerProcessName returns the background-process name a detached task's
// worker is registered under.
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
	ID                  string              `json:"id"`
	Command             string              `json:"command,omitempty"`
	WorkspaceID         string              `json:"workspaceId,omitempty"`
	Status              Status              `json:"status"`
	Phase               string              `json:"phase,omitempty"`
	Step                string              `json:"step,omitempty"`
	OperationID         string              `json:"operationId,omitempty"`
	ParentOperationID   string              `json:"parentOperationId,omitempty"`
	DurationMs          int64               `json:"durationMs,omitempty"`
	Error               string              `json:"error,omitempty"`
	ErrorCode           string              `json:"errorCode,omitempty"`
	ErrorHint           string              `json:"errorHint,omitempty"`
	ErrorContext        map[string]string   `json:"errorContext,omitempty"`
	Result              *config.Result      `json:"result,omitempty"`
	PID                 int                 `json:"pid,omitempty"`
	ProcessTreeIdentity string              `json:"processTreeIdentity,omitempty"`
	Process             *command.ProcessRef `json:"process,omitempty"`
	CancelRequested     bool                `json:"cancelRequested,omitempty"`
	LaunchPending       bool                `json:"launchPending,omitempty"`
	StartedAt           time.Time           `json:"startedAt"`
	UpdatedAt           time.Time           `json:"updatedAt"`
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
	workerLock   *flock.Flock
	launcherLock *flock.Flock
}

func (t *Task) ID() string { return t.id }

func (s *State) Canceled() bool {
	return s != nil && s.Status == StatusFailed && s.ErrorCode == string(clierr.CodeCanceled)
}

func (s *State) ProcessReference() (command.ProcessRef, bool) {
	if s == nil {
		return command.ProcessRef{}, false
	}
	if s.Process != nil {
		if s.Process.PID > 0 {
			return *s.Process, true
		}
	}
	if s.PID <= 0 {
		return command.ProcessRef{}, false
	}
	return command.ProcessRef{
		PID:      s.PID,
		TreeKind: command.ProcessTreeLegacyPID,
		TreeID:   WorkerProcessName(s.ID),
		Identity: s.ProcessTreeIdentity,
	}, true
}

func (t *Task) BeginLaunch() error {
	launcherLock, available, err := t.store.tryLauncherLock(t.id)
	if err != nil {
		return fmt.Errorf("lock task %s launcher: %w", t.id, err)
	}
	if !available {
		return fmt.Errorf("task %s already has a launcher", t.id)
	}
	var canceled bool
	err = t.store.update(t.id, func(s *State) {
		if s.Status.Terminal() || s.CancelRequested {
			canceled = true
			return
		}
		s.LaunchPending = true
	})
	if err != nil {
		_ = launcherLock.Unlock()
		return err
	}
	if canceled {
		_ = launcherLock.Unlock()
		return ErrCanceled
	}
	t.launcherLock = launcherLock
	return nil
}

func (t *Task) FinishLaunch() error {
	err := t.store.update(t.id, func(s *State) { s.LaunchPending = false })
	return errors.Join(err, releaseLauncherLock(t))
}

func (t *Task) SetProcess(ref command.ProcessRef) error {
	if ref.PID <= 0 {
		return fmt.Errorf("invalid worker PID %d", ref.PID)
	}
	err := t.store.update(t.id, func(s *State) {
		s.Process = &ref
		s.PID = ref.PID
		s.ProcessTreeIdentity = ref.Identity
		s.LaunchPending = false
	})
	return errors.Join(err, releaseLauncherLock(t))
}

// SetPID reads legacy task state and tests that publish a worker after launch.
func (t *Task) SetPID(pid int) error {
	identity, err := command.ProcessTreeIdentity(pid)
	if err != nil {
		return fmt.Errorf("identify process tree for pid %d: %w", pid, err)
	}
	return t.SetProcess(command.ProcessRef{
		PID:      pid,
		TreeKind: command.ProcessTreeLegacyPID,
		TreeID:   WorkerProcessName(t.id),
		Identity: identity,
	})
}

// HoldWorkerLock claims this task's worker lock while its worker is active.
// Release it only when startup aborts before work begins.
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

// ReleaseWorkerLock releases the worker claim when startup aborts before work begins.
func (t *Task) ReleaseWorkerLock() error {
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

func releaseLauncherLock(t *Task) error {
	if t.launcherLock == nil {
		return nil
	}
	lock := t.launcherLock
	t.launcherLock = nil
	if err := lock.Unlock(); err != nil {
		return fmt.Errorf("unlock task %s launcher: %w", t.id, err)
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
		if s.Status.Terminal() || s.CancelRequested {
			return
		}
		s.Status = StatusSucceeded
		s.Result = result
		s.Error = ""
		s.ErrorCode = ""
		s.ErrorHint = ""
		s.ErrorContext = nil
	})
}

// Cancel terminates the worker before recording cancellation. A termination
// failure leaves the task retryable.
func (t *Task) Cancel() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return t.CancelContext(ctx)
}

func (t *Task) CancelContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := t.requestCancel()
	if err != nil {
		return err
	}
	if state.Status.Terminal() {
		return nil
	}
	return quiesceCancellation(t, ctx)
}

func quiesceCancellation(t *Task, ctx context.Context) error {
	var terminated *command.ProcessRef
	for {
		retry, newTerminated, err := quiesceCancellationStep(t, ctx, terminated)
		if err != nil {
			return err
		}
		if !retry {
			return nil
		}
		terminated = newTerminated
		if err := waitForCancellation(ctx); err != nil {
			return fmt.Errorf("cancel task %s: %w", t.id, err)
		}
	}
}

func quiesceCancellationStep(
	t *Task,
	ctx context.Context,
	terminated *command.ProcessRef,
) (bool, *command.ProcessRef, error) {
	state, err := t.store.Get(t.id)
	if err != nil {
		return false, nil, fmt.Errorf("cancel task %s: read state: %w", t.id, err)
	}
	if state.Status.Terminal() {
		return false, nil, nil
	}
	finalized, err := tryFinalizeWithoutWorker(t, ctx)
	if err != nil {
		return false, nil, err
	}
	if finalized {
		return false, nil, nil
	}

	retry, ref, err := processCancellationObservation(t, state, terminated, ctx)
	if err != nil {
		return false, nil, err
	}
	if retry {
		return true, terminated, nil
	}
	if ref != nil {
		terminated = ref
	}
	return true, terminated, nil
}

func processCancellationObservation(
	t *Task,
	state *State,
	terminated *command.ProcessRef,
	ctx context.Context,
) (bool, *command.ProcessRef, error) {
	ref, hasRef := state.ProcessReference()
	if hasRef {
		if err := terminateWorker(t, ref, terminated, ctx); err != nil {
			return false, nil, err
		}
		return false, &ref, nil
	}
	observation, err := t.store.WaitForWorkerObservation(ctx, t.id)
	if err != nil {
		return false, nil, fmt.Errorf("cancel task %s: wait for worker startup: %w", t.id, err)
	}
	return observation.WorkerGone, nil, nil
}

func tryFinalizeWithoutWorker(t *Task, ctx context.Context) (bool, error) {
	workerLock, available, err := t.store.tryWorkerLock(t.id)
	if err != nil {
		return false, fmt.Errorf("cancel task %s: inspect worker lock: %w", t.id, err)
	}
	if !available {
		return false, nil
	}
	if t.store.afterCancelLockClaimedHook != nil {
		t.store.afterCancelLockClaimedHook()
	}
	return true, cancelWithoutWorker(t, ctx, workerLock)
}

func terminateWorker(
	t *Task,
	ref command.ProcessRef,
	terminated *command.ProcessRef,
	ctx context.Context,
) error {
	if terminated != nil && *terminated == ref {
		return nil
	}
	if err := t.store.processController.Terminate(ref); err != nil {
		finalized, lockErr := tryFinalizeWithoutWorker(t, ctx)
		if lockErr == nil && finalized {
			return nil
		}
		return fmt.Errorf("cancel task %s: terminate worker tree: %w", t.id, err)
	}
	return nil
}

func cancelWithoutWorker(t *Task, ctx context.Context, workerLock *flock.Flock) error {
	defer func() { _ = workerLock.Unlock() }()
	state, err := waitForLaunch(t, ctx)
	if err != nil {
		return err
	}
	if state.Status.Terminal() {
		return nil
	}
	if ref, ok := state.ProcessReference(); ok {
		if err := t.store.processController.CleanupAfterExit(ref); err != nil {
			return fmt.Errorf("cancel task %s: clean up worker tree: %w", t.id, err)
		}
	}
	return t.finalizeCanceled()
}

func waitForLaunch(t *Task, ctx context.Context) (*State, error) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, done, err := waitForLaunchStep(t)
		if err != nil {
			return nil, err
		}
		if done {
			return state, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf(
				"cancel task %s: wait for launch publication: %w",
				t.id,
				ctx.Err(),
			)
		case <-ticker.C:
		}
	}
}

func waitForLaunchStep(t *Task) (*State, bool, error) {
	state, err := t.store.Get(t.id)
	if err != nil {
		return nil, false, fmt.Errorf("cancel task %s: read state: %w", t.id, err)
	}
	if state.Status.Terminal() || !state.LaunchPending {
		return state, true, nil
	}
	resolved, err := clearAbandonedLaunch(t)
	if err != nil {
		return nil, false, err
	}
	if resolved {
		return nil, false, nil
	}
	return nil, false, nil
}

func clearAbandonedLaunch(t *Task) (bool, error) {
	launcherLock, available, err := t.store.tryLauncherLock(t.id)
	if err != nil {
		return false, fmt.Errorf("cancel task %s: inspect launcher lock: %w", t.id, err)
	}
	if !available {
		return false, nil
	}
	defer func() { _ = launcherLock.Unlock() }()
	err = t.store.update(t.id, func(current *State) {
		if current.CancelRequested && current.LaunchPending {
			current.LaunchPending = false
		}
	})
	if err != nil {
		return false, fmt.Errorf("cancel task %s: clear abandoned launch: %w", t.id, err)
	}
	return true, nil
}

func waitForCancellation(ctx context.Context) error {
	timer := time.NewTimer(20 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func markCanceled(s *State) {
	s.Status = StatusFailed
	s.Error = ErrCanceled.Error()
	s.ErrorCode = string(clierr.CodeCanceled)
	s.ErrorHint = "Retry the operation when ready."
	s.ErrorContext = nil
}

// Fail preserves an existing terminal state, so the error a canceled worker
// reports on its way out doesn't mask ErrCanceled as the reason it stopped.
func (t *Task) Fail(err error) error {
	message := ""
	var classified *clierr.CLIError
	if err != nil {
		if errors.Is(err, ErrCanceled) {
			classified = &clierr.CLIError{
				Code:    clierr.CodeCanceled,
				Message: ErrCanceled.Error(),
				Hint:    "Retry the operation when ready.",
			}
		} else {
			classified = clierr.Classify(err)
		}
		message = classified.Message
	}
	redactor := secrets.NewEnvironmentRedactor(os.Environ())
	return t.store.update(t.id, func(s *State) {
		if s.Status.Terminal() || s.CancelRequested {
			return
		}
		s.Status = StatusFailed
		s.Error = message
		if classified != nil {
			s.Error = redactor.Redact(message)
			s.ErrorCode = redactor.Redact(string(classified.Code))
			s.ErrorHint = redactor.Redact(classified.Hint)
			s.ErrorContext = redactContext(classified.Context, redactor)
		}
	})
}

func (t *Task) requestCancel() (*State, error) {
	var snapshot *State
	err := t.store.update(t.id, func(state *State) {
		if !state.Status.Terminal() {
			state.CancelRequested = true
		}
		stateSnapshot := *state
		snapshot = &stateSnapshot
	})
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (t *Task) finalizeCanceled() error {
	return t.store.update(t.id, func(state *State) {
		if !state.Status.Terminal() && state.CancelRequested {
			markCanceled(state)
			state.LaunchPending = false
		}
	})
}

type taskReporter struct {
	task *Task
}

func (r taskReporter) Report(e status.Event) {
	redactor := secrets.NewEnvironmentRedactor(os.Environ())
	_ = r.task.store.update(r.task.id, func(s *State) {
		// A terminal task is done being described; late events from a worker
		// still unwinding must not resurrect it or rewrite its outcome.
		if s.Status.Terminal() || s.CancelRequested {
			return
		}
		if e.State == status.StateFailed {
			s.Status = StatusFailed
			s.Phase = redactor.Redact(string(e.Phase))
			s.Step = redactor.Redact(e.Step)
			s.OperationID = redactor.Redact(e.OperationID)
			s.ParentOperationID = redactor.Redact(e.ParentOperationID)
			s.DurationMs = e.Duration.Milliseconds()
			if e.Error != nil {
				s.Error = redactor.Redact(e.Error.Message)
				s.ErrorCode = redactor.Redact(e.Error.Code)
				s.ErrorHint = redactor.Redact(e.Error.Hint)
				s.ErrorContext = redactContext(e.Error.Context, redactor)
			}
			if recoverableFailure(e.Phase) {
				if s.Status == StatusFailed {
					s.Status = StatusRunning
				}
				return
			}
			return
		}
		if s.Status == StatusPending {
			s.Status = StatusRunning
		}
		s.Phase = redactor.Redact(string(e.Phase))
		s.Step = redactor.Redact(e.Step)
		s.OperationID = redactor.Redact(e.OperationID)
		s.ParentOperationID = redactor.Redact(e.ParentOperationID)
	})
}

// recoverableFailure identifies optional workspace-up phases whose failures
// are followed by a documented fallback path. They remain visible in status
// output but must not prevent the detached task from recording eventual
// success.
func recoverableFailure(phase status.Phase) bool {
	switch phase {
	case status.PhaseStartingSSHTunnel, status.PhaseConfiguringSSH:
		return true
	default:
		return false
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
