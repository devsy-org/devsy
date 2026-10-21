package up

import (
	"context"
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
	t, err := store.CreateLaunch(task.CreateOptions{
		Command:     "up",
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return err
	}

	if err := launchDetached(t, cmd.detachedInvocationEnvironment()); err != nil {
		return errors.Join(fmt.Errorf("launch detached up: %w", err), t.FailLaunch(err))
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
	if err := workspace2.ValidateID(cmd.ID); err != nil {
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
		Name:            task.WorkerProcessName(t.ID()),
		Lease:           t.LaunchLease(),
		AtomicOwnership: true,
		OnPrepared:      t.PrepareProcessOwnership,
		OnStarted: func(ref command.ProcessRef) error {
			if err := t.PublishProcess(ref); err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return t.CommitLaunch(ctx)
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
		return nil, err
	}
	if err := waitForTaskLaunch(t, store); err != nil {
		return nil, finalizeWorkerTask(t, err)
	}
	return t, nil
}

func waitForTaskLaunch(t *task.Task, store *task.Store) error {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	leaseRetained := false
	for {
		state, err := readWorkerLaunchState(t, store)
		if err != nil {
			return err
		}
		ready, err := advanceTaskLaunch(t, store, state, &leaseRetained)
		if err != nil {
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-deadline.C:
			return errors.New("timed out waiting for detached worker launch metadata")
		case <-ticker.C:
		}
	}
}

func readWorkerLaunchState(t *task.Task, store *task.Store) (*task.State, error) {
	state, err := store.Get(t.ID())
	if err != nil {
		return nil, err
	}
	if state.Status.Terminal() || state.CancelRequested {
		return nil, task.ErrCanceled
	}
	return state, nil
}

func advanceTaskLaunch(
	t *task.Task,
	store *task.Store,
	state *task.State,
	leaseRetained *bool,
) (bool, error) {
	if !state.LaunchPending {
		if _, ok := state.ProcessReference(); !ok {
			return false, errors.New("detached worker launch did not publish process metadata")
		}
		return true, nil
	}
	if state.LaunchProtocol != 1 {
		return false, nil
	}
	if err := retainWorkerLaunchLease(leaseRetained); err != nil {
		return false, err
	}
	if err := checkWorkerLauncher(t, store); err != nil {
		return false, err
	}
	if state.PID != 0 && !state.WorkerReady {
		return false, t.WorkerLaunchReady(os.Getpid())
	}
	return false, nil
}

func retainWorkerLaunchLease(retained *bool) error {
	if *retained {
		return nil
	}
	if err := command.RetainLaunchLease(); err != nil {
		return err
	}
	*retained = true
	return nil
}

func checkWorkerLauncher(t *task.Task, store *task.Store) error {
	alive, err := t.LauncherAlive()
	if err != nil {
		return err
	}
	if alive {
		return nil
	}
	latest, err := store.Get(t.ID())
	if err != nil {
		return err
	}
	if latest.LaunchPending {
		return errors.New("detached worker launcher exited before commit")
	}
	return nil
}

func succeedTask(t *task.Task, result *config2.Result) error {
	if t == nil {
		return nil
	}
	return t.Succeed(result)
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
