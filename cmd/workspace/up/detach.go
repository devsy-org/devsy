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
	if err := cmd.checkDetachedPresentation(cfg, workspaceID); err != nil {
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

func (cmd *UpCmd) checkDetachedPresentation(cfg *config.Config, workspaceID string) error {
	if !cmd.hasPresentation() {
		return nil
	}
	ctx := context.Background()
	existed := workspaceID == "" ||
		workspace2.Exists(ctx, cfg, nil, workspaceID, cmd.Owner) != ""
	return cmd.checkPresentation(cfg, workspaceID, existed)
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
		traceDetachedAdmission("worker lock")
		return nil, err
	}
	verifiedOwner, err := waitForTaskLaunch(t, store)
	if err != nil {
		if verifiedOwner {
			return nil, finalizeWorkerTask(t, err)
		}
		if releaseErr := t.ReleaseWorkerLock(); releaseErr != nil {
			err = errors.Join(err, fmt.Errorf("release detached worker lock: %w", releaseErr))
		}
		return nil, err
	}
	return t, nil
}

type workerLaunchProgress struct {
	leaseRetained bool
	verifiedOwner bool
}

func waitForTaskLaunch(t *task.Task, store *task.Store) (bool, error) {
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	progress := workerLaunchProgress{}
	for {
		state, err := readWorkerLaunchState(t, store)
		if err != nil {
			traceDetachedAdmission("reading launch state")
			return progress.verifiedOwner, err
		}
		ready, err := advanceTaskLaunch(t, store, state, &progress)
		if err != nil {
			return progress.verifiedOwner, err
		}
		if ready {
			return progress.verifiedOwner, nil
		}
		select {
		case <-deadline.C:
			traceDetachedAdmission("waiting for launch commit")
			return progress.verifiedOwner, errors.New(
				"timed out waiting for detached worker launch metadata",
			)
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
	progress *workerLaunchProgress,
) (bool, error) {
	owner, err := verifyWorkerTaskOwner(state)
	progress.verifiedOwner = owner
	if err != nil {
		traceDetachedAdmission("verifying owner")
		return false, err
	}
	if !state.LaunchPending {
		return true, nil
	}
	if state.LaunchProtocol != 1 {
		progress.verifiedOwner = false
		return false, nil
	}
	if err := retainWorkerLaunchLease(&progress.leaseRetained); err != nil {
		traceDetachedAdmission("retaining launch lease")
		return false, err
	}
	if err := checkWorkerLauncher(t, store); err != nil {
		traceDetachedAdmission("checking launcher")
		return false, err
	}
	if state.PID != 0 && !state.WorkerReady {
		if err := t.WorkerLaunchReady(os.Getpid()); err != nil {
			traceDetachedAdmission("acknowledging worker")
			return false, err
		}
		return false, nil
	}
	return false, nil
}

func traceDetachedAdmission(stage string) {
	if os.Getenv("DEVSY_DETACHED_DIAGNOSTICS") == "1" {
		fmt.Fprintln(os.Stderr, "detached worker admission failed:", stage)
	}
}

// The worker lock excludes concurrent invocations; only the saved kernel identity
// authorizes this invocation to acknowledge launch or persist startup failures.
func verifyWorkerTaskOwner(state *task.State) (bool, error) {
	ref, ok := state.ProcessReference()
	if !ok {
		if state.LaunchPending {
			return false, nil
		}
		return false, errors.New("detached worker launch did not publish process metadata")
	}
	if ref.PID != os.Getpid() || ref.Identity == "" {
		return false, errors.New("detached task belongs to a different or unverified worker")
	}
	identity, err := command.ProcessTreeIdentity(os.Getpid())
	if err != nil {
		return false, fmt.Errorf("verify detached worker identity: %w", err)
	}
	if identity == "" || identity != ref.Identity {
		return false, errors.New("detached worker identity does not match task ownership")
	}
	return true, nil
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
