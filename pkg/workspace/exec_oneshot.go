package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/platform"
)

// ExecOneShotOptions configures a single non-interactive command execution
// inside a workspace's running container.
type ExecOneShotOptions struct {
	WorkspaceName         string
	Command               []string
	Workdir               string
	Env                   map[string]string
	IDLabels              []string // additional id-labels for container lookup; nil uses defaults
	TimeoutSeconds        int
	TimeoutSecondsDefault int
	TimeoutSecondsMax     int
	Owner                 platform.OwnerFilter
	Context               string
	Provider              string
	Stdout                io.Writer
	Stderr                io.Writer
}

// defaultExecTimeoutSeconds bounds an exec when no caller or configured
// default applies. Tuned to surface hung calls without truncating typical
// build/test commands.
const defaultExecTimeoutSeconds = 300

// ExecOneShotResult is the structured outcome of an exec.
type ExecOneShotResult struct {
	ExitCode       int
	DurationMS     int64
	TimedOut       bool
	TimeoutSeconds int
	Clamped        bool
}

// ResolveTimeout picks the first positive of TimeoutSeconds, TimeoutSecondsDefault,
// fallbackDefault, then clamps by TimeoutSecondsMax. The bool reports a clamp.
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
		return nil, fmt.Errorf("workspace name is required")
	}
	if len(opts.Command) == 0 {
		return nil, fmt.Errorf("command is required")
	}

	timeout, clamped := opts.ResolveTimeout(defaultExecTimeoutSeconds)

	// Resolve under the parent context so docker lookup doesn't eat the exec budget.
	resolved, err := resolveExecTarget(ctx, opts)
	if err != nil {
		return nil, err
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	exitCode, runErr := execOneShotWithRuntime(execCtx, resolved.runtime, ExecRequest{
		Target:  resolved.target,
		Workdir: resolved.workdir,
		Env:     resolved.envMap,
		Argv:    opts.Command,
		Stdout:  opts.Stdout,
		Stderr:  opts.Stderr,
	})
	duration := time.Since(start)

	res := &ExecOneShotResult{
		ExitCode:       exitCode,
		DurationMS:     duration.Milliseconds(),
		Clamped:        clamped,
		TimeoutSeconds: int(timeout.Seconds()),
	}
	// Parent error first — execCtx inherits its cancellation, so we'd otherwise
	// misreport caller cancellation as our own timeout.
	if parentErr := ctx.Err(); parentErr != nil {
		res.ExitCode = -1
		return res, parentErr
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

// execOneShotWithRuntime is the low-level exec path; it accepts an already-
// resolved runtime and request so tests can inject a fake runtime without
// touching workspace resolution.
func execOneShotWithRuntime(
	ctx context.Context,
	runtime ContainerRuntime,
	req ExecRequest,
) (int, error) {
	return runtime.Exec(ctx, req)
}

// resolvedExecTarget bundles the resolved runtime, target, workdir, and env
// for an exec — kept as a struct to stay within revive's function-result-limit.
type resolvedExecTarget struct {
	runtime ContainerRuntime
	target  ContainerTarget
	workdir string
	envMap  map[string]string
}

// resolveExecTarget resolves the container runtime, target, workdir, and env
// map from options.
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
	runtime := NewDockerRuntime(workspaceConfig, "")

	containerDetails, err := runtime.FindRunning(
		ctx, devcontainer.GetRunnerIDFromWorkspace(workspaceConfig), opts.IDLabels,
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
		ContainerID: containerDetails.ID,
		User:        user,
	}

	userEnvProbe := ""
	if execResult != nil && execResult.MergedConfig != nil {
		userEnvProbe = execResult.MergedConfig.UserEnvProbe
	}
	probedEnv := runtime.ProbeEnv(ctx, target, userEnvProbe)
	envSlice := envMapToSlice(opts.Env)
	envMap := BuildExecEnv(execResult, envSlice, probedEnv)

	return resolvedExecTarget{
		runtime: runtime,
		target:  target,
		workdir: workdir,
		envMap:  envMap,
	}, nil
}

func envMapToSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}
