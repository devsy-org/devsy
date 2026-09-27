package workspace

import (
	"errors"
	"fmt"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/task"
)

// upTaskCommand is the command label detached up workers are created with.
const upTaskCommand = "up"

// QuiesceUpTasks cancels active detached up tasks for the workspace. It tries
// every match and returns all failures because a survivor can restart it.
func QuiesceUpTasks(store *task.Store, workspaceID string) error {
	return quiesceUpTasks(store, workspaceID, func(id string) error {
		return store.Open(id).Cancel()
	})
}

func quiesceUpTasks(
	store *task.Store,
	workspaceID string,
	cancelTask func(id string) error,
) error {
	active, err := store.ActiveForWorkspace(workspaceID, upTaskCommand)
	if err != nil {
		return fmt.Errorf("list active up tasks for workspace %s: %w", workspaceID, err)
	}

	var errs []error
	for _, state := range active {
		if err := cancelTask(state.ID); err != nil {
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

// QuiesceUpTasksForWorkspace quiesces the workspace's active up tasks
// against the default task store.
func QuiesceUpTasksForWorkspace(workspaceID string) error {
	store, err := task.NewStore()
	if err != nil {
		return fmt.Errorf("open task store: %w", err)
	}
	return QuiesceUpTasks(store, workspaceID)
}
