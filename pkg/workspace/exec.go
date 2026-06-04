package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/platform"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

const (
	DefaultDockerCommand   = "docker"
	ContainerStatusRunning = "running"
)

// defaultExecTimeoutSeconds bounds an exec when no caller or configured
// default applies. Tuned to surface hung calls without truncating typical
// build/test commands.
const defaultExecTimeoutSeconds = 300

// ----------------------------------------------------------------------------
// Workspace metadata helpers (runtime-agnostic).
// ----------------------------------------------------------------------------

// ResolveDockerCommand returns the docker binary to invoke. Precedence:
// caller-supplied override → provider config (agent.docker.path) → default.
// Callers wired to a --docker-path style flag pass it as override; the override
// is honored even when workspace is nil so flag handling works at every call site.
func ResolveDockerCommand(
	workspace *provider2.Workspace,
	override string,
) string {
	if override != "" {
		return override
	}
	if workspace == nil || workspace.Context == "" {
		return DefaultDockerCommand
	}

	providerConfig, err := provider2.LoadProviderConfig(
		workspace.Context,
		workspace.Provider.Name,
	)
	if err != nil {
		log.Debugf("Failed to load provider config, defaulting to 'docker': %v", err)
		return DefaultDockerCommand
	}

	if providerConfig.Agent.Docker.Path != "" {
		if expanded := os.ExpandEnv(providerConfig.Agent.Docker.Path); expanded != "" {
			return expanded
		}
	}

	return DefaultDockerCommand
}

func LoadExecResult(
	workspaceConfig *provider2.Workspace,
	containerDetails *devcconfig.ContainerDetails,
) *devcconfig.Result {
	if workspaceConfig == nil || workspaceConfig.Context == "" || workspaceConfig.ID == "" {
		return nil
	}

	result, err := provider2.LoadWorkspaceResult(workspaceConfig.Context, workspaceConfig.ID)
	if err != nil {
		log.Warnf("Error loading workspace result: %v", err)
		return nil
	}
	if result != nil {
		result.ContainerDetails = containerDetails
	}
	return result
}

func ResolveExecWorkdir(result *devcconfig.Result, workspaceName string) string {
	if result != nil && result.MergedConfig != nil && result.MergedConfig.WorkspaceFolder != "" {
		return result.MergedConfig.WorkspaceFolder
	}
	return path.Join("/workspaces", workspaceName)
}

// BuildExecEnv merges probed env, result remote env, and caller-supplied env slices.
func BuildExecEnv(
	result *devcconfig.Result,
	cliEnv []string,
	probedEnv map[string]string,
) map[string]string {
	env := make(map[string]string, len(probedEnv))
	maps.Copy(env, probedEnv)

	if result != nil {
		applyRemoteEnv(env, mergedRemoteEnv(result))
	}

	for _, e := range cliEnv {
		if k, v, ok := strings.Cut(e, "="); ok {
			env[k] = v
		}
	}

	return env
}

func mergedRemoteEnv(result *devcconfig.Result) map[string]*string {
	merged := map[string]*string{}
	if result.MergedConfig != nil {
		maps.Copy(merged, result.MergedConfig.RemoteEnv)
	}
	if result.DevContainerConfigWithPath != nil && result.DevContainerConfigWithPath.Config != nil {
		maps.Copy(merged, result.DevContainerConfigWithPath.Config.RemoteEnv)
	}
	return merged
}

func applyRemoteEnv(env map[string]string, remoteEnv map[string]*string) {
	for k, v := range remoteEnv {
		if v == nil {
			delete(env, k)
		} else {
			env[k] = *v
		}
	}
}

// probeShellFlag returns the shell flag for the given probe mode.
func probeShellFlag(probe devcconfig.UserEnvProbe) string {
	switch probe {
	case devcconfig.LoginInteractiveShellProbe:
		return "-lic"
	case devcconfig.LoginShellProbe:
		return "-lc"
	case devcconfig.InteractiveShellProbe:
		return "-ic"
	default:
		return "-c"
	}
}

// buildProbeArgs constructs the docker exec arguments for env probing.
func buildProbeArgs(target ContainerTarget, shellFlag string, cmd string) []string {
	args := []string{"exec"}
	if target.User != "" {
		args = append(args, "--user", target.User)
	}
	args = append(args, target.ContainerID, "sh", shellFlag, cmd)
	return args
}

// parseEnvOutput parses the output of an env probe command.
func parseEnvOutput(out []byte, sep byte) map[string]string {
	entries := bytes.Split(out, []byte{sep})
	env := make(map[string]string, len(entries))
	for _, e := range entries {
		if len(e) == 0 {
			continue
		}
		name, value, ok := bytes.Cut(e, []byte{'='})
		if !ok || len(name) == 0 {
			continue
		}
		env[string(name)] = string(value)
	}
	delete(env, "PWD")
	return env
}

// ----------------------------------------------------------------------------
// ContainerRuntime — abstraction over docker / podman / test doubles.
// ----------------------------------------------------------------------------

// ContainerRuntime abstracts container-runtime operations used for workspace
// exec and env-probe. Callers do not need to know whether Docker, Podman, or a
// test double is underneath.
type ContainerRuntime interface {
	// FindRunning looks up a single running container for the given workspace.
	// workspaceID may be empty when only idLabels are provided.
	FindRunning(
		ctx context.Context,
		workspaceID string,
		idLabels []string,
	) (*devcconfig.ContainerDetails, error)

	// Exec runs the command described by req inside a container. It writes
	// stdout and stderr to req.Stdout/req.Stderr and returns the process exit
	// code. A non-nil error means the exec machinery itself failed (e.g. the
	// docker binary could not be found), not that the command exited non-zero.
	Exec(ctx context.Context, req ExecRequest) (exitCode int, err error)

	// ProbeEnv queries the container's environment using the given probe mode
	// string (values defined by devcconfig.UserEnvProbe). Returns an empty map
	// on any error.
	ProbeEnv(ctx context.Context, target ContainerTarget, probeMode string) map[string]string
}

// ExecRequest bundles all parameters for a single non-interactive container
// exec. Using a struct keeps the ContainerRuntime.Exec signature within the
// revive argument-limit rule while remaining extensible.
type ExecRequest struct {
	Target  ContainerTarget
	Workdir string
	Env     map[string]string
	Argv    []string
	Stdout  io.Writer
	Stderr  io.Writer
}

// ContainerTarget identifies a single container exec target. Runtime-agnostic
// data; runtimes consume it.
type ContainerTarget struct {
	ContainerID string
	User        string
}

// ----------------------------------------------------------------------------
// DockerRuntime — production implementation of ContainerRuntime.
// ----------------------------------------------------------------------------

// DockerRuntime shells out to a docker-compatible binary.
type DockerRuntime struct {
	helper *docker.DockerHelper
}

// NewDockerRuntime constructs a runtime that shells out to the docker-like
// binary chosen by ResolveDockerCommand. The override parameter is honored
// the same way ResolveDockerCommand honors it.
func NewDockerRuntime(workspace *provider2.Workspace, override string) *DockerRuntime {
	return &DockerRuntime{
		helper: &docker.DockerHelper{
			DockerCommand: ResolveDockerCommand(workspace, override),
		},
	}
}

// DockerCommand exposes the resolved docker binary path (for diagnostics or
// callers that still need the raw string).
func (r *DockerRuntime) DockerCommand() string { return r.helper.DockerCommand }

// FindRunning implements ContainerRuntime.
func (r *DockerRuntime) FindRunning(
	ctx context.Context,
	workspaceID string,
	idLabels []string,
) (*devcconfig.ContainerDetails, error) {
	labels := devcconfig.GetIDLabels(workspaceID, idLabels)
	container, err := r.helper.FindDevContainer(ctx, labels)
	if err != nil {
		return nil, fmt.Errorf("find container: %w", err)
	}
	if container == nil {
		return nil, fmt.Errorf(
			"no running container found for workspace %q",
			workspaceID,
		)
	}

	if !strings.EqualFold(container.State.Status, ContainerStatusRunning) {
		return nil, fmt.Errorf(
			"container %s is not running (status: %s)",
			container.ID,
			container.State.Status,
		)
	}

	return container, nil
}

// Exec implements ContainerRuntime.
func (r *DockerRuntime) Exec(ctx context.Context, req ExecRequest) (int, error) {
	execArgs := []string{"exec", "-i"}
	for k, v := range req.Env {
		execArgs = append(execArgs, "-e", k+"="+v)
	}
	if req.Workdir != "" {
		execArgs = append(execArgs, "--workdir", req.Workdir)
	}
	if req.Target.User != "" {
		execArgs = append(execArgs, "--user", req.Target.User)
	}
	execArgs = append(execArgs, req.Target.ContainerID)
	execArgs = append(execArgs, req.Argv...)

	stdout := req.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	stderr := req.Stderr
	if stderr == nil {
		stderr = io.Discard
	}

	err := r.helper.Run(ctx, execArgs, nil, stdout, stderr)
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return -1, fmt.Errorf("exec in container %s: %w", req.Target.ContainerID, err)
}

// ProbeEnv implements ContainerRuntime.
func (r *DockerRuntime) ProbeEnv(
	ctx context.Context,
	target ContainerTarget,
	probe string,
) map[string]string {
	userEnvProbe, err := devcconfig.NewUserEnvProbe(probe)
	if err != nil {
		log.Warnf("Invalid userEnvProbe %q, using default: %v", probe, err)
		userEnvProbe = devcconfig.DefaultUserEnvProbe
	}
	if userEnvProbe == devcconfig.NoneProbe {
		return map[string]string{}
	}

	shellFlag := probeShellFlag(userEnvProbe)

	out, sep, probeErr := r.runProbeCommand(ctx, target, shellFlag)
	if probeErr != nil {
		log.Warnf("Failed to probe user env: %v", probeErr)
		return map[string]string{}
	}
	return parseEnvOutput(out, sep)
}

func (r *DockerRuntime) runProbeCommand(
	ctx context.Context,
	target ContainerTarget,
	shellFlag string,
) ([]byte, byte, error) {
	args := buildProbeArgs(target, shellFlag, "cat /proc/self/environ")
	var stdout bytes.Buffer
	err := r.helper.Run(ctx, args, nil, &stdout, io.Discard)
	if err == nil {
		return stdout.Bytes(), 0, nil
	}

	log.Debugf("Env probe with /proc/self/environ failed: %v, trying printenv", err)
	args = buildProbeArgs(target, shellFlag, "printenv")
	stdout.Reset()
	err = r.helper.Run(ctx, args, nil, &stdout, io.Discard)
	if err != nil {
		return nil, 0, fmt.Errorf("probe user env: %w", err)
	}
	return stdout.Bytes(), '\n', nil
}

// ----------------------------------------------------------------------------
// ExecOneShot — the high-level non-interactive exec entry point.
// ----------------------------------------------------------------------------

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
