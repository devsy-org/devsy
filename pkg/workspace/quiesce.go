package workspace

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/task"
)

// upTaskCommand is the command label detached up workers are created with.
const upTaskCommand = "up"

const upTaskCancellationTimeout = 5 * time.Second

// QuiesceUpTasks cancels active detached up tasks for the workspace. It tries
// every match and returns all failures because a survivor can restart it.
func QuiesceUpTasks(store *task.Store, workspaceID string) error {
	return QuiesceUpTasksContext(context.Background(), store, workspaceID)
}

func QuiesceUpTasksContext(ctx context.Context, store *task.Store, workspaceID string) error {
	return quiesceUpTasks(ctx, store, workspaceID, func(ctx context.Context, id string) error {
		return store.Open(id).CancelContext(ctx)
	})
}

func quiesceUpTasks(
	ctx context.Context,
	store *task.Store,
	workspaceID string,
	cancelTask func(ctx context.Context, id string) error,
) error {
	states, err := store.ForWorkspace(workspaceID, upTaskCommand)
	if err != nil {
		return fmt.Errorf("list up tasks for workspace %s: %w", workspaceID, err)
	}

	var errs []error
	handled := 0
	for _, state := range states {
		if state.Status.Terminal() && !state.NeedsExitedWorkerCleanup() &&
			!state.NeedsCanceledWorkerCleanup() {
			continue
		}
		taskCtx, cancel := context.WithTimeout(ctx, upTaskCancellationTimeout)
		err := quiesceTask(taskCtx, store, state, cancelTask)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("quiesce up task %s: %w", state.ID, err))
			continue
		}
		handled++
	}
	if handled > 0 {
		log.Debugf(
			"quiesced up tasks for workspace %s: tasks=%d failures=%d",
			workspaceID,
			handled,
			len(errs),
		)
	}
	return errors.Join(errs...)
}

// A reconciled task is terminal, so cancelling it is a no-op, yet its crashed
// worker may have left descendants that would restart the workspace.
func quiesceTask(
	ctx context.Context,
	store *task.Store,
	state *task.State,
	cancelTask func(ctx context.Context, id string) error,
) error {
	if !state.Status.Terminal() {
		if err := cancelTask(ctx, state.ID); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.CleanupCanceledWorkerTree(ctx, state.ID); err != nil {
		return err
	}
	return store.CleanupExitedWorkerTree(state.ID)
}

type UpTaskQuiescer interface {
	Quiesce(context.Context, string) error
}

type StoreUpTaskQuiescer struct{}

func (StoreUpTaskQuiescer) Quiesce(ctx context.Context, workspaceID string) error {
	store, err := task.NewStore()
	if err != nil {
		return fmt.Errorf("open task store: %w", err)
	}
	return QuiesceUpTasksContext(ctx, store, workspaceID)
}

// QuiesceUpTasksForWorkspace quiesces the workspace's active up tasks
// against the default task store.
func QuiesceUpTasksForWorkspace(workspaceID string) error {
	return StoreUpTaskQuiescer{}.Quiesce(context.Background(), workspaceID)
}
