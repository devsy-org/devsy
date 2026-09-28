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
	active, err := store.ActiveForWorkspace(workspaceID, upTaskCommand)
	if err != nil {
		return fmt.Errorf("list active up tasks for workspace %s: %w", workspaceID, err)
	}

	var errs []error
	for _, state := range active {
		taskCtx, cancel := context.WithTimeout(ctx, upTaskCancellationTimeout)
		err := cancelTask(taskCtx, state.ID)
		cancel()
		if err != nil {
			errs = append(errs, fmt.Errorf("cancel up task %s: %w", state.ID, err))
		}
	}
	if len(active) > 0 {
		log.Debugf(
			"quiesced up tasks for workspace %s: tasks=%d failures=%d",
			workspaceID,
			len(active),
			len(errs),
		)
	}
	return errors.Join(errs...)
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
