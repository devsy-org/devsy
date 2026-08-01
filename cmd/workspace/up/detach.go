package up

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/devsy-org/devsy/pkg/command"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/task"
)

// runDetached submits this invocation as a background task and returns
// immediately.
func (cmd *UpCmd) runDetached(args []string) error {
	store, err := task.NewStore()
	if err != nil {
		return err
	}
	t, err := store.Create(task.CreateOptions{
		Command:     "up",
		WorkspaceID: cmd.detachWorkspaceLabel(args),
	})
	if err != nil {
		return err
	}

	if err := launchDetached(t.ID()); err != nil {
		_ = t.Fail(err)
		return fmt.Errorf("launch detached up: %w", err)
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	if mode == output.ModeJSON {
		return config2.WriteTaskJSON(cmd.stdout(), t.ID())
	}
	_, err = fmt.Fprintf(cmd.stdout(),
		"Submitted task %s. Poll with 'workspace task get %s' or 'workspace task logs %s -f'.\n",
		t.ID(), t.ID(), t.ID())
	return err
}

func launchDetached(taskID string) error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}

	args := append(detachedArgs(os.Args[1:]), names.Flag(names.TaskID), taskID)
	return command.StartBackground("devsy-up-"+taskID, func() (*exec.Cmd, error) {
		return &exec.Cmd{
			Path: execPath,
			Args: append([]string{execPath}, args...),
			Env:  os.Environ(),
			Dir:  wd(),
		}, nil
	})
}

// detachedArgs strips --detach so it isn't duplicated alongside --task-id.
func detachedArgs(args []string) []string {
	detachFlag := names.Flag(names.Detach)
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == detachFlag || strings.HasPrefix(a, detachFlag+"=") || a == "-d" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// detachWorkspaceLabel is a best-effort label for task list.
func (cmd *UpCmd) detachWorkspaceLabel(args []string) string {
	if cmd.ID != "" {
		return cmd.ID
	}
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

func wd() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	return dir
}

// openTask returns nil when this invocation was not launched via detach.
func (cmd *UpCmd) openTask() (*task.Task, error) {
	if cmd.taskID == "" {
		return nil, nil
	}
	store, err := task.NewStore()
	if err != nil {
		return nil, err
	}
	t := store.Open(cmd.taskID)
	// obtain the worker lock first.
	if err := t.HoldWorkerLock(); err != nil {
		failTask(t, err)
		return nil, err
	}
	if err := t.SetPID(os.Getpid()); err != nil {
		failTask(t, err)
		return nil, err
	}
	return t, nil
}

func failTask(t *task.Task, err error) {
	if t == nil {
		return
	}
	_ = t.Fail(err)
}

func succeedTask(t *task.Task, result *config2.Result) {
	if t == nil {
		return
	}
	_ = t.Succeed(result)
}
