package workspace

import (
	"errors"
	"fmt"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/task"
)

// upTaskCommand is the command label detached up workers are created with.
const upTaskCommand = "up"

// QuiesceUpTasks cancels every persisted, still-active detached up task for
// the workspace so no surviving worker can mutate or restart it during
// stop/delete. Every matching task is attempted even when one fails;
// failures are aggregated, never logged and ignored, because a surviving
// worker is a known local mutator capable of reversing the lifecycle change.
func QuiesceUpTasks(store *task.Store, workspaceID string) error {
	active, err := store.ActiveForWorkspace(workspaceID, upTaskCommand)
	if err != nil {
		return fmt.Errorf("list active up tasks for workspace %s: %w", workspaceID, err)
	}

	var errs []error
	for _, state := range active {
		if err := store.Open(state.ID).Cancel(); err != nil {
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

// QuiesceUpTasksForWorkspace resolves the default task store and quiesces
// the workspace's active up tasks.
func QuiesceUpTasksForWorkspace(workspaceID string) error {
	store, err := task.NewStore()
	if err != nil {
		return fmt.Errorf("open task store: %w", err)
	}
	return QuiesceUpTasks(store, workspaceID)
}
