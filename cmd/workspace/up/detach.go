package up

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/file"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/secrets"
	"github.com/devsy-org/devsy/pkg/task"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
)

// runDetached submits this invocation as a background task and returns
// immediately.
func (cmd *UpCmd) runDetached(args []string, cfg *config.Config) error {
	// Return unlock failures to the submitting process before creating a task.
	// Desktop can then prompt and retry the submission with scoped credentials.
	if err := cmd.prepareDetached(cfg); err != nil {
		return err
	}

	store, err := task.NewStore()
	if err != nil {
		return err
	}
	workspaceID, err := cmd.detachWorkspaceLabel(args)
	if err != nil {
		return err
	}
	t, err := store.Create(task.CreateOptions{
		Command:     "up",
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return err
	}

	if err := t.BeginLaunch(); err != nil {
		_ = t.Fail(err)
		return fmt.Errorf("prepare detached up: %w", err)
	}
	if err := launchDetached(t, cmd.detachedInvocationEnvironment()); err != nil {
		_ = t.FinishLaunch()
		_ = t.Fail(err)
		return fmt.Errorf("launch detached up: %w", err)
	}
	return cmd.renderDetachedTask(t)
}

func (cmd *UpCmd) renderDetachedTask(t *task.Task) error {
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

func (cmd *UpCmd) prepareDetached(cfg *config.Config) error {
	if err := mergeDevsyUpOptions(&cmd.CLIOptions); err != nil {
		return err
	}
	return cmd.preflightLocalValues(cfg)
}

func launchDetached(t *task.Task, invocationEnv []string) error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}

	args := append(detachedArgs(os.Args[1:]), names.Flag(names.TaskID), t.ID())
	return command.StartSupervisedBackground(command.SupervisedStartOptions{
		Name: task.WorkerProcessName(t.ID()),
		OnStarted: func(ref command.ProcessRef) error {
			return t.SetProcess(ref)
		},
	}, func() (*exec.Cmd, error) {
		return &exec.Cmd{
			Path: execPath,
			Args: append([]string{execPath}, args...),
			Env:  invocationEnv,
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

// detachWorkspaceLabel derives the ID stop and delete use before worker startup.
func (cmd *UpCmd) detachWorkspaceLabel(args []string) (string, error) {
	if cmd.FromSnapshot != "" {
		if _, err := cmd.resolveExplicitSource(); err != nil {
			return "", err
		}
	}
	if cmd.ID != "" {
		return cmd.ID, nil
	}
	args = cmd.ensureArgs(args)
	if len(args) > 0 {
		_, source := file.IsLocalDir(args[0])
		return workspace2.ToID(source), nil
	}
	return "", nil
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
	if err := t.HoldWorkerLock(); err != nil {
		failTask(t, err)
		return nil, err
	}
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := store.Get(cmd.taskID)
		if err != nil {
			failTask(t, err)
			_ = t.ReleaseWorkerLock()
			return nil, err
		}
		if state.Status.Terminal() || state.CancelRequested {
			if err := t.ReleaseWorkerLock(); err != nil {
				return nil, fmt.Errorf("release canceled worker lock: %w", err)
			}
			return nil, task.ErrCanceled
		}
		if !state.LaunchPending {
			if _, ok := state.ProcessReference(); !ok {
				err := errors.New("detached worker launch did not publish process metadata")
				failTask(t, err)
				_ = t.ReleaseWorkerLock()
				return nil, err
			}
			return t, nil
		}
		select {
		case <-deadline.C:
			err := errors.New("timed out waiting for detached worker launch metadata")
			failTask(t, err)
			_ = t.ReleaseWorkerLock()
			return nil, err
		case <-ticker.C:
		}
	}
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

func (cmd *UpCmd) detachedInvocationEnvironment() []string {
	env := os.Environ()
	if cmd.secretOptions == nil {
		return env
	}
	cache, ok := cmd.secretOptions.UnlockResolver.(interface {
		Material() (secrets.UnlockMaterial, bool)
	})
	if !ok {
		return env
	}
	material, ok := cache.Material()
	if !ok {
		return env
	}
	scoped := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if name != secrets.EnvPassphrase && name != secrets.EnvPassphraseFile {
			scoped = append(scoped, entry)
		}
	}
	return append(scoped, secrets.EnvPassphrase+"="+material.Passphrase)
}
