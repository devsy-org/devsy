package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/command"
)

func (s *Store) launchLeasePath(id string) (string, error) {
	path, err := s.path(id)
	if err != nil {
		return "", err
	}
	return path + ".launch.lease", nil
}

func (t *Task) LaunchLease() *command.LaunchLease { return t.launchLease }

func (t *Task) PrepareProcessOwnership(treeID string) error {
	accepted := false
	err := t.store.update(t.id, func(s *State) {
		if !s.acceptsLaunch() || s.LaunchOwned {
			return
		}
		s.LaunchOwned = true
		s.LaunchTreeID = treeID
		accepted = true
	})
	if err != nil {
		return err
	}
	if !accepted {
		return ErrCanceled
	}
	return nil
}

func (t *Task) FailLaunch(cause error) error {
	failErr := t.recordFailure(cause, true)
	releaseErr := releaseLauncherLock(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, cleanupErr := t.store.reconcileLaunch(ctx, t.id, true)
	return errors.Join(failErr, releaseErr, cleanupErr)
}

// PublishProcess retains launcher ownership until the worker acknowledges the
// matching process reference and commit authorizes workspace work.
func (t *Task) PublishProcess(ref command.ProcessRef) error {
	if ref.PID <= 0 || ref.Identity == "" {
		return errors.New("worker launch requires a strong process reference")
	}
	accepted := false
	err := t.store.update(t.id, func(s *State) {
		if !s.acceptsLaunch() || s.Process != nil {
			return
		}
		s.Process = &ref
		s.PID = ref.PID
		s.ProcessTreeIdentity = ref.Identity
		accepted = true
	})
	if err != nil {
		return err
	}
	if !accepted {
		return ErrCanceled
	}
	return nil
}

func (t *Task) WorkerLaunchReady(pid int) error {
	identity, err := command.ProcessTreeIdentity(pid)
	if err != nil {
		return err
	}
	accepted := false
	err = t.store.update(t.id, func(s *State) {
		if !s.acceptsLaunch() || s.PID != pid || identity == "" ||
			s.ProcessTreeIdentity != identity {
			return
		}
		s.WorkerReady = true
		accepted = true
	})
	if err != nil {
		return err
	}
	if !accepted {
		return ErrCanceled
	}
	return nil
}

func (t *Task) CommitLaunch(ctx context.Context) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		committed, err := t.commitLaunchStep()
		if err != nil {
			return err
		}
		if committed {
			return releaseLauncherLock(t)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (t *Task) LauncherAlive() (bool, error) {
	lock, available, err := t.store.tryLauncherLock(t.id)
	if err != nil {
		return false, err
	}
	if available {
		return false, lock.Unlock()
	}
	return true, nil
}

func (s *Store) reconcilePendingLaunch(ctx context.Context, id string) (*State, error) {
	return s.reconcileLaunch(ctx, id, false)
}

func (s *Store) reconcileLaunch(ctx context.Context, id string, aborting bool) (*State, error) {
	lock, available, err := s.tryLauncherLock(id)
	if err != nil {
		return nil, err
	}
	if !available {
		return s.Get(id)
	}
	defer func() { _ = lock.Unlock() }()
	state, err := s.Get(id)
	if err != nil || (!state.LaunchPending && !aborting) {
		return state, err
	}
	if state.ProcessCleanupComplete {
		return state, nil
	}
	if err := s.cleanupPendingLaunch(ctx, state); err != nil {
		return state, err
	}
	return s.finishAbandonedLaunch(id, aborting)
}

func (s *Store) cleanupPendingLaunch(ctx context.Context, state *State) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if state.LaunchProtocol != 1 {
		return errors.New("cannot verify cleanup of a legacy abandoned launch")
	}
	if ref, ok := state.ProcessReference(); ok {
		if err := s.processController.Terminate(ref); err != nil {
			return err
		}
		if err := s.processController.CleanupAfterExit(ref); err != nil {
			return err
		}
	}
	path, err := s.launchLeasePath(state.ID)
	if err != nil {
		return err
	}
	if err := command.CleanupUnpublishedLaunch(
		path,
		state.LaunchTreeID,
		state.LaunchOwned,
	); err != nil {
		return fmt.Errorf("clean up abandoned launch %s: %w", state.ID, err)
	}
	return nil
}

func (s *Store) finishAbandonedLaunch(id string, aborting bool) (*State, error) {
	err := s.update(id, func(current *State) {
		if !current.LaunchPending && !aborting {
			return
		}
		current.LaunchPending = false
		current.ProcessCleanupComplete = true
		if current.Status.Terminal() {
			return
		}
		if current.CancelRequested {
			markCanceled(current)
			return
		}
		failure := clierr.Classify(ErrAbandoned)
		current.Status = StatusFailed
		current.Error = failure.Message
		current.ErrorCode = string(failure.Code)
		current.ErrorHint = failure.Hint
	})
	if err != nil {
		return nil, err
	}
	return s.Get(id)
}

func (s *State) acceptsLaunch() bool {
	return s.LaunchProtocol == 1 && s.LaunchPending && !s.Status.Terminal() && !s.CancelRequested
}

func (s *State) hasPendingLaunch() bool { return s != nil && s.LaunchPending }

func (s *State) markLaunchCleanupPending() {
	if s.LaunchProtocol == 1 && !s.ProcessCleanupComplete {
		s.LaunchPending = true
	}
}

func (t *Task) commitLaunchStep() (bool, error) {
	committed, rejected := false, false
	err := t.store.update(t.id, func(s *State) {
		if !s.acceptsLaunch() || s.Process == nil {
			rejected = true
			return
		}
		if !s.WorkerReady {
			return
		}
		s.LaunchPending = false
		committed = true
	})
	if err != nil {
		return false, err
	}
	if rejected {
		return false, ErrCanceled
	}
	return committed, nil
}

func (t *Task) claimLaunchOwnership() error {
	launcherLock, available, err := t.store.tryLauncherLock(t.id)
	if err != nil {
		return fmt.Errorf("lock task %s launcher: %w", t.id, err)
	}
	if !available {
		return fmt.Errorf("task %s already has a launcher", t.id)
	}
	leasePath, err := t.store.launchLeasePath(t.id)
	if err != nil {
		_ = launcherLock.Unlock()
		return err
	}
	lease, err := command.NewLaunchLease(leasePath)
	if err != nil {
		_ = launcherLock.Unlock()
		return err
	}
	t.launcherLock = launcherLock
	t.launchLease = lease
	return nil
}

func (s *State) checkDeletion(force bool) error {
	if s.LaunchPending || s.NeedsExitedWorkerCleanup() || s.NeedsCanceledWorkerCleanup() {
		return fmt.Errorf("task %s still needs process tree cleanup", s.ID)
	}
	if !force && !s.Status.Terminal() {
		return fmt.Errorf(
			"task %s is still %s; cancel it first or delete with force",
			s.ID,
			s.Status,
		)
	}
	return nil
}

func (s *Store) stateAfterLaunchCleanup(id string) (*State, error) {
	state, err := s.Get(id)
	if err != nil || !state.LaunchPending {
		return state, err
	}
	state, err = s.reconcilePendingLaunch(context.Background(), id)
	if err != nil {
		return nil, err
	}
	if state.LaunchPending {
		return nil, fmt.Errorf("task %s launch is still owned", id)
	}
	return state, nil
}

func (s *Store) pendingLaunchState(state *State) *State {
	current, err := s.reconcilePendingLaunch(context.Background(), state.ID)
	if err != nil {
		return state
	}
	return current
}
