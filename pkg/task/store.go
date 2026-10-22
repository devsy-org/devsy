package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/random"
	"github.com/gofrs/flock"
	"tailscale.com/atomicfile"
)

// lockTimeout bounds how long update waits to acquire a task's file lock.
const lockTimeout = 5 * time.Second

// Store persists task state as one JSON file per task under dir.
type Store struct {
	dir                        string
	processController          command.ProcessController
	afterClaimHook             func()
	afterCancelLockClaimedHook func()
	afterStateLockBlockedHook  func()
	stateLockTimeout           time.Duration
}

func NewStore() (*Store, error) {
	dir, err := config.DefaultPathManager().TaskDir()
	if err != nil {
		return nil, fmt.Errorf("task dir: %w", err)
	}
	return NewStoreAt(dir)
}

func NewStoreAt(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create task dir: %w", err)
	}
	return &Store{
		dir:               dir,
		processController: command.DefaultProcessController(),
	}, nil
}

func (s *Store) Create(opts CreateOptions) (*Task, error) {
	return s.create(opts, false)
}

// CreateLaunch acquires launch ownership before the task becomes visible.
func (s *Store) CreateLaunch(opts CreateOptions) (*Task, error) {
	return s.create(opts, true)
}

// Open returns a handle without reading the task's state.
func (s *Store) Open(id string) *Task {
	return &Task{store: s, id: id}
}

// Delete cleans any unfinished terminal process tree before removing its
// record. It rejects a non-terminal task unless force is set.
func (s *Store) Delete(id string, force bool) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.CleanupCanceledWorkerTree(ctx, id); err != nil {
		return err
	}
	if err := s.CleanupExitedWorkerTree(id); err != nil {
		return err
	}
	// Locked across the check and the removal so a concurrent update can't
	// commit its atomic rename after the check and recreate the task.
	return s.withLock(id, func() error {
		state, err := s.readState(id)
		if err != nil {
			return err
		}
		if err := state.checkDeletion(force); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("delete task %s: %w", id, err)
		}
		return nil
	})
}

func (s *Store) Get(id string) (*State, error) {
	var state *State
	err := s.withLock(id, func() error {
		var err error
		state, err = s.readState(id)
		return err
	})
	return state, err
}

// Abandoned reports whether a non-terminal task's worker is gone.
func (s *Store) Abandoned(state *State) bool {
	if state == nil || state.Status.Terminal() {
		return false
	}
	lock, ok := s.claimDeadWorkerLock(state.ID)
	if !ok {
		return false
	}
	_ = lock.Unlock()
	return true
}

// ReconcileTask cleans up a task whose worker exited before recording its
// result, then records it as canceled or abandoned.
func (s *Store) ReconcileTask(ctx context.Context, id string) (*State, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if current.LaunchPending {
		return s.reconcilePendingLaunch(ctx, id)
	}
	if !current.NeedsReconciliation() {
		return current, nil
	}
	lock, ok := s.claimDeadWorkerLock(id)
	if !ok {
		return current, nil
	}
	defer func() { _ = lock.Unlock() }()
	if s.afterClaimHook != nil {
		s.afterClaimHook()
	}
	return s.reconcileClaimedTask(ctx, id)
}

// ReconcileState marks a task after its worker exits without a result. A
// requested cancellation completes cleanup here; an abandoned task's tree is
// cleaned up by the next workspace stop or delete.
func (s *Store) ReconcileState(state *State) *State {
	if state.hasPendingLaunch() {
		return s.pendingLaunchState(state)
	}
	if !state.NeedsReconciliation() {
		return state
	}
	lock, ok := s.claimDeadWorkerLock(state.ID)
	if !ok {
		return state
	}
	defer func() { _ = lock.Unlock() }()
	current, err := s.Get(state.ID)
	if err != nil {
		return state
	}
	if !current.NeedsReconciliation() {
		return current
	}
	if current.CancelRequested {
		reconciled, err := s.reconcileClaimedCancellation(context.Background(), current)
		if err != nil {
			return current
		}
		return reconciled
	}
	return s.failAbandoned(current)
}

// CleanupExitedWorkerTree tears down any descendants a dead worker left behind.
// Claiming the worker lock proves no worker is running, so a saved reference
// that still points at live processes belongs to a crashed worker's tree.
func (s *Store) CleanupExitedWorkerTree(id string) error {
	state, err := s.stateAfterLaunchCleanup(id)
	if err != nil {
		return err
	}
	if !state.NeedsExitedWorkerCleanup() {
		return nil
	}
	lock, ok := s.claimDeadWorkerLock(id)
	if !ok {
		return fmt.Errorf("cannot claim dead worker lock for abandoned task %s", id)
	}
	defer func() { _ = lock.Unlock() }()
	state, err = s.Get(id)
	if err != nil {
		return err
	}
	if !state.NeedsExitedWorkerCleanup() {
		return nil
	}
	ref, _ := state.ProcessReference()
	if err := s.processController.CleanupAfterExit(ref); err != nil {
		return fmt.Errorf("clean up task %s process tree: %w", id, err)
	}
	return s.markProcessCleanupComplete(id)
}

// CleanupCanceledWorkerTree handles records that were marked canceled before
// their worker tree was terminated. A successful termination of an identityless
// tree is sufficient only while the worker lock proves it was still live.
func (s *Store) CleanupCanceledWorkerTree(ctx context.Context, id string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	state, err := s.Get(id)
	if err != nil {
		return err
	}
	if !state.NeedsCanceledWorkerCleanup() {
		return nil
	}
	ref, _ := state.ProcessReference()
	return s.waitCanceledWorkerCleanup(ctx, id, ref)
}

func hasProcessReference(state *State) bool {
	_, ok := state.ProcessReference()
	return ok
}

// List returns every known task, most recently started first.
func (s *Store) List() ([]*State, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read task dir: %w", err)
	}

	states := make([]*State, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-len(".json")]
		var state *State
		err := s.withLock(id, func() error {
			var readErr error
			state, readErr = s.readState(id)
			// Only an absent state read under the lock confirms concurrent deletion.
			if errors.Is(readErr, os.ErrNotExist) {
				return nil
			}
			return readErr
		})
		if err != nil {
			return nil, fmt.Errorf("list task %s: %w", id, err)
		}
		if state != nil {
			states = append(states, state)
		}
	}

	sort.Slice(states, func(i, j int) bool {
		return states[i].StartedAt.After(states[j].StartedAt)
	})
	return states, nil
}

// ForWorkspace returns matching tasks, newest first, without changing state.
func (s *Store) ForWorkspace(workspaceID, command string) ([]*State, error) {
	states, err := s.List()
	if err != nil {
		return nil, err
	}
	matching := make([]*State, 0, len(states))
	for _, state := range states {
		if state.WorkspaceID == workspaceID && (command == "" || state.Command == command) {
			matching = append(matching, state)
		}
	}
	return matching, nil
}

// ActiveForWorkspace returns non-terminal tasks for a workspace, newest first.
func (s *Store) ActiveForWorkspace(workspaceID, command string) ([]*State, error) {
	states, err := s.ForWorkspace(workspaceID, command)
	if err != nil {
		return nil, err
	}

	active := make([]*State, 0, len(states))
	for _, state := range states {
		if state.Status.Terminal() {
			continue
		}
		active = append(active, state)
	}
	return active, nil
}

type WorkerObservation struct {
	State      *State
	WorkerGone bool
}

// WaitForWorkerObservation waits until process metadata is published, the
// task becomes terminal, or its worker lock becomes available.
func (s *Store) WaitForWorkerObservation(
	ctx context.Context,
	id string,
) (WorkerObservation, error) {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		observation, ready, err := s.workerObservation(id)
		if err != nil {
			return WorkerObservation{}, err
		}
		if ready {
			return observation, nil
		}
		select {
		case <-ctx.Done():
			return WorkerObservation{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// The caller must hold the state lock: Windows replacement can briefly make
// the path unavailable, and lock-owning callbacks cannot acquire it again.
func (s *Store) readState(id string) (*State, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	file, err := openTaskFile(path)
	if err != nil {
		return nil, fmt.Errorf("read task %s: %w", id, err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read task %s: %w", id, err)
	}
	state := &State{}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("parse task %s: %w", id, err)
	}
	return state, nil
}

func (s *Store) markProcessCleanupComplete(id string) error {
	return s.update(id, func(state *State) {
		state.ProcessCleanupComplete = true
	})
}

func (s *Store) waitCanceledWorkerCleanup(
	ctx context.Context,
	id string,
	ref command.ProcessRef,
) error {
	terminated := false
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		complete, nowTerminated, err := s.cleanupCanceledWorkerTreeStep(id, ref, terminated)
		if err != nil || complete {
			return err
		}
		terminated = nowTerminated
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Store) cleanupCanceledWorkerTreeStep(
	id string,
	ref command.ProcessRef,
	terminated bool,
) (complete, nowTerminated bool, err error) {
	lock, available, err := s.tryWorkerLock(id)
	if err != nil {
		return false, terminated, fmt.Errorf("inspect canceled task %s worker lock: %w", id, err)
	}
	if available {
		defer func() { _ = lock.Unlock() }()
		return true, terminated, s.finishCanceledWorkerCleanup(id, ref, terminated)
	}
	if !terminated {
		if err := s.processController.Terminate(ref); err != nil {
			return false, false, fmt.Errorf("terminate canceled task %s process tree: %w", id, err)
		}
		terminated = true
	}
	return false, terminated, nil
}

func (s *Store) finishCanceledWorkerCleanup(
	id string,
	ref command.ProcessRef,
	terminated bool,
) error {
	current, err := s.Get(id)
	if err != nil || !current.NeedsCanceledWorkerCleanup() {
		return err
	}
	if !terminated || ref.Identity != "" {
		if err := s.processController.CleanupAfterExit(ref); err != nil {
			return fmt.Errorf("clean up canceled task %s process tree: %w", id, err)
		}
	}
	return s.markProcessCleanupComplete(id)
}

func (s *Store) workerObservation(id string) (WorkerObservation, bool, error) {
	state, err := s.Get(id)
	if err != nil {
		return WorkerObservation{}, false, err
	}
	if state.Status.Terminal() || hasProcessReference(state) {
		return WorkerObservation{State: state}, true, nil
	}
	lock, available, err := s.tryWorkerLock(id)
	if err != nil {
		return WorkerObservation{}, false, err
	}
	if !available {
		return WorkerObservation{}, false, nil
	}
	_ = lock.Unlock()
	return WorkerObservation{State: state, WorkerGone: true}, true, nil
}

func (s *Store) reconcileClaimedTask(ctx context.Context, id string) (*State, error) {
	current, err := s.Get(id)
	if err != nil || !current.NeedsReconciliation() {
		return current, err
	}
	if current.CancelRequested {
		return s.reconcileClaimedCancellation(ctx, current)
	}
	cleaned := false
	if ref, ok := current.ProcessReference(); ok {
		if err := ctx.Err(); err != nil {
			return current, err
		}
		if err := s.processController.CleanupAfterExit(ref); err != nil {
			return current, fmt.Errorf("clean up task %s process tree: %w", id, err)
		}
		cleaned = true
	}
	return s.finishClaimedReconciliation(current, cleaned)
}

func (s *Store) reconcileClaimedCancellation(ctx context.Context, current *State) (*State, error) {
	if err := ctx.Err(); err != nil {
		return current, err
	}
	if ref, ok := current.ProcessReference(); ok {
		if err := s.processController.CleanupAfterExit(ref); err != nil {
			return current, fmt.Errorf(
				"clean up canceled task %s process tree: %w", current.ID, err,
			)
		}
	}
	if err := s.Open(current.ID).finalizeCanceled(); err != nil {
		return current, err
	}
	return s.Get(current.ID)
}

func (s *Store) finishClaimedReconciliation(current *State, cleaned bool) (*State, error) {
	current = s.failAbandoned(current)
	if cleaned && current.Status == StatusFailed && current.Error == ErrAbandoned.Error() {
		if err := s.markProcessCleanupComplete(current.ID); err != nil {
			return current, err
		}
		return s.Get(current.ID)
	}
	return current, nil
}

// failAbandoned records ErrAbandoned for a task whose worker is confirmed gone.
// Must be called with the worker lock held.
func (s *Store) failAbandoned(state *State) *State {
	current, err := s.Get(state.ID)
	if err != nil {
		return state
	}
	if current.Status.Terminal() {
		return current
	}
	if err := s.Open(state.ID).Fail(ErrAbandoned); err != nil {
		return current
	}
	reconciled, err := s.Get(state.ID)
	if err != nil {
		return current
	}
	return reconciled
}

// tryWorkerLock claims the task's worker lock when no worker owns it. Unlike
// claimDeadWorkerLock, it creates the lock file if the worker has not started.
func (s *Store) tryWorkerLock(id string) (*flock.Flock, bool, error) {
	return s.tryLock(id, s.workerLockPath)
}

func (s *Store) tryLauncherLock(id string) (*flock.Flock, bool, error) {
	return s.tryLock(id, s.launcherLockPath)
}

func (s *Store) tryLock(
	id string,
	lockPath func(string) (string, error),
) (*flock.Flock, bool, error) {
	path, err := lockPath(id)
	if err != nil {
		return nil, false, err
	}
	lock := flock.New(path)
	locked, err := lock.TryLock()
	if err != nil {
		return nil, false, err
	}
	if !locked {
		return nil, false, nil
	}
	return lock, true, nil
}

// claimDeadWorkerLock claims reconciliation ownership after a worker releases
// its lock or its saved identity proves it exited before claiming one.
func (s *Store) claimDeadWorkerLock(id string) (*flock.Flock, bool) {
	path, err := s.workerLockPath(id)
	if err != nil {
		return nil, false
	}
	_, statErr := os.Stat(path)
	if errors.Is(statErr, os.ErrNotExist) {
		// Reconciliation must never manufacture evidence that a worker claimed its lock.
		return claimConfirmedWorkerExit(
			path+".reconcile",
			func() bool { return s.workerExitConfirmed(id) },
		)
	}
	if statErr != nil {
		return nil, false
	}
	lock := flock.New(path)
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return nil, false
	}
	return lock, true
}

func claimConfirmedWorkerExit(path string, confirmed func() bool) (*flock.Flock, bool) {
	if !confirmed() {
		return nil, false
	}
	lock := flock.New(path)
	locked, err := lock.TryLock()
	if err != nil || !locked {
		return nil, false
	}
	if !confirmed() {
		_ = lock.Unlock()
		return nil, false
	}
	return lock, true
}

func (s *Store) workerExitConfirmed(id string) bool {
	state, err := s.Get(id)
	if err != nil {
		return false
	}
	ref, ok := state.ProcessReference()
	if !ok {
		return false
	}
	exited, err := command.ProcessExited(ref)
	return err == nil && exited
}

// workerLockPath is distinct from the read-modify-write lock in withLock: this
// one is held for the worker's entire lifetime, so sharing it would block
// every state update.
func (s *Store) workerLockPath(id string) (string, error) {
	path, err := s.path(id)
	if err != nil {
		return "", err
	}
	return path + ".worker.lock", nil
}

func (s *Store) launcherLockPath(id string) (string, error) {
	path, err := s.path(id)
	if err != nil {
		return "", err
	}
	return path + ".launcher.lock", nil
}

func (s *Store) update(id string, mutate func(*State)) error {
	return s.withLock(id, func() error {
		state, err := s.readState(id)
		if err != nil {
			return err
		}
		mutate(state)
		state.touch()
		return s.write(state)
	})
}

// withLock runs fn holding an OS file lock scoped to id, so a concurrent
// read-modify-write from another process (or another Store instance in this
// one) can't interleave with it.
func (s *Store) withLock(id string, fn func() error) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}

	lock := flock.New(path + ".lock")
	locked, err := lock.TryLock()
	if err != nil {
		return fmt.Errorf("lock task %s: %w", id, err)
	}
	if locked {
		defer func() { _ = lock.Unlock() }()
		return fn()
	}
	if s.afterStateLockBlockedHook != nil {
		s.afterStateLockBlockedHook()
	}
	timeout := s.stateLockTimeout
	if timeout == 0 {
		timeout = lockTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	locked, err = lock.TryLockContext(ctx, 20*time.Millisecond)
	if err != nil {
		return fmt.Errorf("lock task %s: %w", id, err)
	}
	if !locked {
		return fmt.Errorf("lock task %s: timed out", id)
	}
	defer func() { _ = lock.Unlock() }()

	return fn()
}

// path rejects any id that isn't a single clean path component, so a
// caller-supplied id like "../../etc/passwd" can't escape s.dir.
func (s *Store) path(id string) (string, error) {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id {
		return "", fmt.Errorf("invalid task id %q", id)
	}
	return filepath.Join(s.dir, id+".json"), nil
}

// write atomically replaces the task's state file so a concurrent Get never
// observes a partially written file.
func (s *Store) write(state *State) error {
	data, err := marshalState(state)
	if err != nil {
		return err
	}

	target, err := s.path(state.ID)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, state.ID+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp task file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write task state: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp task file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp task file: %w", err)
	}
	if err := atomicfile.Rename(tmpPath, target); err != nil {
		return fmt.Errorf("commit task state: %w", err)
	}
	syncDir(s.dir)
	return nil
}

// syncDir fsyncs a directory so a rename survives a crash.
func syncDir(dir string) {
	d, err := os.Open(dir) // #nosec G304 -- store's own task dir.
	if err != nil {
		return
	}
	defer func() { _ = d.Close() }()
	_ = d.Sync()
}

func (s *Store) create(opts CreateOptions, launch bool) (*Task, error) {
	id := random.String(12)
	t := &Task{store: s, id: id}
	if launch {
		if err := t.claimLaunchOwnership(); err != nil {
			return nil, err
		}
	}
	now := time.Now()
	state := &State{
		ID:          id,
		Command:     opts.Command,
		WorkspaceID: opts.WorkspaceID,
		Status:      StatusPending,
		StartedAt:   now,
		UpdatedAt:   now,
	}
	if launch {
		state.LaunchPending = true
		state.LaunchProtocol = 1
	}
	if err := s.withLock(id, func() error { return s.write(state) }); err != nil {
		return nil, errors.Join(err, releaseLauncherLock(t))
	}
	return t, nil
}
