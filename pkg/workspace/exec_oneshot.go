package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/platform"
)

// ExecOneShotOptions configures a single non-interactive command execution
// inside a workspace's running container.
type ExecOneShotOptions struct {
	WorkspaceName         string
	Command               []string
	Workdir               string
	Env                   map[string]string
	TimeoutSeconds        int
	TimeoutSecondsDefault int
	TimeoutSecondsMax     int
	Owner                 platform.OwnerFilter
	Context               string
	Provider              string
	Stdout                io.Writer
	Stderr                io.Writer
}

// ExecOneShotResult is the structured outcome of an exec.
type ExecOneShotResult struct {
	ExitCode      int
	DurationMS    int64
	TimedOut      bool
	TimeoutSecond int
	Clamped       bool
}

// ResolveTimeout returns the effective timeout and whether the caller's
// requested value was clamped down by Max. Precedence:
//  1. TimeoutSeconds if > 0
//  2. TimeoutSecondsDefault if > 0
//  3. fallbackDefault
//
// The result is clamped by TimeoutSecondsMax if > 0.
func (o ExecOneShotOptions) ResolveTimeout(fallbackDefault int) (time.Duration, bool) {
	want := o.TimeoutSeconds
	if want <= 0 {
		want = o.TimeoutSecondsDefault
	}
	if want <= 0 {
		want = fallbackDefault
	}
	if o.TimeoutSecondsMax > 0 && want > o.TimeoutSecondsMax {
		return time.Duration(o.TimeoutSecondsMax) * time.Second, true
	}
	return time.Duration(want) * time.Second, false
}

// ExecOneShot runs Command inside the workspace's container, captures
// stdout/stderr via the provided writers, and returns a structured result.
// It never reads stdin and never allocates a TTY.
func ExecOneShot(ctx context.Context, opts ExecOneShotOptions) (*ExecOneShotResult, error) {
	if opts.WorkspaceName == "" {
		return nil, fmt.Errorf("WorkspaceName is required")
	}
	if len(opts.Command) == 0 {
		return nil, fmt.Errorf("command is required")
	}

	timeout, clamped := opts.ResolveTimeout(300)
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resolved, err := resolveExecTarget(execCtx, opts)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	exitCode, runErr := runCapture(execCtx, captureArgs{
		target:  resolved.target,
		workdir: resolved.workdir,
		env:     resolved.envMap,
		command: opts.Command,
		stdout:  opts.Stdout,
		stderr:  opts.Stderr,
	})
	duration := time.Since(start)

	res := &ExecOneShotResult{
		ExitCode:      exitCode,
		DurationMS:    duration.Milliseconds(),
		Clamped:       clamped,
		TimeoutSecond: int(timeout.Seconds()),
	}
	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
		res.ExitCode = -1
		return res, nil
	}
	if runErr != nil {
		return res, runErr
	}
	return res, nil
}

// resolvedExecTarget bundles the resolved container target, workdir, and env for an exec.
type resolvedExecTarget struct {
	target  ContainerTarget
	workdir string
	envMap  map[string]string
}

// resolveExecTarget resolves the container target, workdir, and env map from options.
func resolveExecTarget(ctx context.Context, opts ExecOneShotOptions) (resolvedExecTarget, error) {
	devsyConfig, err := config.LoadConfig(opts.Context, opts.Provider)
	if err != nil {
		return resolvedExecTarget{}, fmt.Errorf("load config: %w", err)
	}

	client, err := Get(ctx, GetOptions{
		DevsyConfig: devsyConfig,
		Args:        []string{opts.WorkspaceName},
		Owner:       opts.Owner,
	})
	if err != nil {
		return resolvedExecTarget{}, fmt.Errorf("resolve workspace: %w", err)
	}

	workspaceConfig := client.WorkspaceConfig()
	dockerCommand := ResolveDockerCommand(workspaceConfig)

	containerDetails, err := FindRunningContainer(
		ctx, dockerCommand, devcontainer.GetRunnerIDFromWorkspace(workspaceConfig), nil,
	)
	if err != nil {
		return resolvedExecTarget{}, err
	}

	execResult := LoadExecResult(workspaceConfig, containerDetails)
	workdir := opts.Workdir
	if workdir == "" {
		workdir = ResolveExecWorkdir(execResult, client.Workspace())
	}

	user := ""
	if execResult != nil {
		user = devcconfig.GetRemoteUser(execResult)
	}

	target := ContainerTarget{
		Helper:      &docker.DockerHelper{DockerCommand: dockerCommand},
		ContainerID: containerDetails.ID,
		User:        user,
	}
	probedEnv := ProbeContainerEnv(ctx, target, "")
	envSlice := envMapToSlice(opts.Env)
	envMap := BuildExecEnv(execResult, envSlice, probedEnv)

	return resolvedExecTarget{target: target, workdir: workdir, envMap: envMap}, nil
}

// captureArgs bundles the arguments for runCapture.
type captureArgs struct {
	target  ContainerTarget
	workdir string
	env     map[string]string
	command []string
	stdout  io.Writer
	stderr  io.Writer
}

func runCapture(ctx context.Context, args captureArgs) (int, error) {
	execArgs := []string{"exec", "-i"}
	for k, v := range args.env {
		execArgs = append(execArgs, "-e", k+"="+v)
	}
	if args.workdir != "" {
		execArgs = append(execArgs, "--workdir", args.workdir)
	}
	if args.target.User != "" {
		execArgs = append(execArgs, "--user", args.target.User)
	}
	execArgs = append(execArgs, args.target.ContainerID)
	execArgs = append(execArgs, args.command...)

	stdout := args.stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := args.stderr
	if stderr == nil {
		stderr = io.Discard
	}

	err := args.target.Helper.Run(ctx, execArgs, nil, stdout, stderr)
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return -1, err
}

func envMapToSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
