package devcontainer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
)

// initializeCommand carries the shared execution state for the initializeCommand
// hook: the host shell, working directory, and extra environment.
type initializeCommand struct {
	shell           []string
	workspaceFolder string
	extraEnv        []string
}

// runInitializeCommand executes the devcontainer.json initializeCommand hook on
// the host. Named sub-commands run concurrently; their errors are collected and
// returned together.
func runInitializeCommand(
	ctx context.Context,
	workspaceFolder string,
	conf *config.DevContainerConfig,
	extraEnv []string,
) error {
	if len(conf.InitializeCommand) == 0 {
		return nil
	}

	init := &initializeCommand{
		shell:           hostShell(),
		workspaceFolder: workspaceFolder,
		extraEnv:        extraEnv,
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for name, cmd := range conf.InitializeCommand {
		wg.Go(func() {
			if err := init.run(ctx, name, cmd); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	return errors.Join(errs...)
}

// hostShell returns the shell invocation used to run string-form commands.
// initializeCommand runs on the host; on Windows sh may not be on PATH, so we
// fall back to the default command interpreter.
func hostShell() []string {
	if runtime.GOOS != "windows" {
		return []string{"sh", "-c"}
	}
	if comSpec := os.Getenv("COMSPEC"); comSpec != "" {
		return []string{comSpec, "/c"}
	}
	return []string{"cmd.exe", "/c"}
}

// run executes a single named sub-command. A single-element command is run as a
// shell string; a multi-element command is executed argv-style.
func (c *initializeCommand) run(ctx context.Context, name string, cmd []string) error {
	if len(cmd) == 0 {
		newInitRecording(ctx, name).record(workspacelogs.InvocationFailed, "empty_command")
		return fmt.Errorf("initializeCommand %q is empty", name)
	}

	args := cmd
	if len(cmd) == 1 {
		args = append(append([]string{}, c.shell...), cmd[0])
	}

	log.Debugf(
		"Running initializeCommand %q from devcontainer.json: %q",
		name,
		strings.Join(args, " "),
	)

	stdout := log.Writer(log.LevelInfo)
	stderr := log.Writer(log.LevelError)
	defer func() { _ = stdout.Close() }()
	defer func() { _ = stderr.Close() }()

	// args come from devcontainer.json initializeCommand, a trusted local config.
	command := exec.CommandContext(ctx, args[0], args[1:]...) //nolint:gosec // G204
	command.Dir = c.workspaceFolder
	command.Env = append(command.Environ(), c.extraEnv...)
	command.Stdout = stdout
	command.Stderr = stderr

	// Recording tees before the log writers filter by level.
	rec := newInitRecording(ctx, name)
	if rec != nil {
		command.Stdout = io.MultiWriter(stdout, rec.stdout)
		command.Stderr = io.MultiWriter(stderr, rec.stderr)
		rec.record(workspacelogs.InvocationRunning, "")
	}

	started := time.Now()
	err := command.Run()
	rec.finish(ctx, command.ProcessState, time.Since(started), err)
	if err != nil {
		return fmt.Errorf("initializeCommand %q failed: %w", name, err)
	}
	return nil
}

// initRecording retains one initializeCommand sub-command's output and
// outcome. A nil value records nothing.
type initRecording struct {
	rec            *workspacelogs.Recorder
	inv            workspacelogs.Invocation
	stdout, stderr io.WriteCloser
}

func newInitRecording(ctx context.Context, name string) *initRecording {
	if ctx == nil {
		return nil
	}
	rec := workspacelogs.RecorderFrom(ctx)
	if rec == nil {
		return nil
	}
	id := workspacelogs.NewID()

	return &initRecording{
		rec: rec,
		inv: workspacelogs.Invocation{
			ID: id, Phase: "lifecycle", Hook: "initializeCommand", Key: name,
		},
		stdout: rec.Writer(id, workspacelogs.StreamStdout),
		stderr: rec.Writer(id, workspacelogs.StreamStderr),
	}
}

func (r *initRecording) record(state workspacelogs.InvocationState, reason string) {
	if r == nil {
		return
	}
	inv := r.inv
	inv.State = state
	inv.Reason = reason
	r.rec.Invocation(inv)
}

// finish flushes output, then records the terminal state.
func (r *initRecording) finish(
	ctx context.Context,
	state *os.ProcessState,
	elapsed time.Duration,
	runErr error,
) {
	if r == nil {
		return
	}
	_ = r.stdout.Close()
	_ = r.stderr.Close()
	inv := r.inv
	inv.Duration = elapsed
	inv.State, inv.Reason = initOutcome(ctx, state, runErr)
	if state != nil {
		if sig, ok := strings.CutPrefix(state.String(), "signal: "); ok {
			inv.Signal = sig
		} else if code := state.ExitCode(); code >= 0 {
			inv.ExitCode = &code
		}
	}
	r.rec.Invocation(inv)
}

func initOutcome(
	ctx context.Context,
	state *os.ProcessState,
	runErr error,
) (workspacelogs.InvocationState, string) {
	switch {
	case runErr == nil:
		return workspacelogs.InvocationSucceeded, ""
	case ctx.Err() != nil:
		return workspacelogs.InvocationCanceled, ""
	case state == nil:
		return workspacelogs.InvocationFailed, "start_failed"
	default:
		return workspacelogs.InvocationFailed, ""
	}
}
