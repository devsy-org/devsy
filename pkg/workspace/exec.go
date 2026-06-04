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

// defaultExecTimeoutSeconds bounds an exec when no caller or configured default
// applies. Long enough for typical build/test, short enough to surface hangs.
const defaultExecTimeoutSeconds = 300

// ResolveDockerCommand returns the docker binary to invoke. Precedence:
// override → provider config (agent.docker.path) → default. The override is
// honored even when workspace is nil.
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

func buildProbeArgs(target ContainerTarget, shellFlag string, cmd string) []string {
	args := []string{"exec"}
	if target.User != "" {
		args = append(args, "--user", target.User)
	}
	args = append(args, target.ContainerID, "sh", shellFlag, cmd)
	return args
}

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

// ContainerRuntime abstracts find/exec/probe over a container runtime so
// callers and tests don't depend on a particular CLI (docker, podman, ...).
type ContainerRuntime interface {
	// FindRunning resolves a running container by workspace ID and/or labels.
	// A non-nil error includes the not-found and not-running cases.
	FindRunning(
		ctx context.Context,
		workspaceID string,
		idLabels []string,
	) (*devcconfig.ContainerDetails, error)

	// Exec runs req inside a container and returns the process exit code.
	// A non-nil error means the exec machinery itself failed (e.g. binary
	// missing), not a non-zero exit.
	Exec(ctx context.Context, req ExecRequest) (exitCode int, err error)

	// ProbeEnv reads the container's environment via shell. Returns an empty
	// map on any failure; probeMode comes from devcconfig.UserEnvProbe.
	ProbeEnv(ctx context.Context, target ContainerTarget, probeMode string) map[string]string
}

// ExecRequest is the per-call input to ContainerRuntime.Exec.
type ExecRequest struct {
	Target  ContainerTarget
	Workdir string
	Env     map[string]string
	Argv    []string
	Stdout  io.Writer
	Stderr  io.Writer
}

// ContainerTarget identifies a container and the user to exec as.
type ContainerTarget struct {
	ContainerID string
	User        string
}

// DockerRuntime is the production ContainerRuntime, shelling out to a
// docker-compatible binary.
type DockerRuntime struct {
	helper *docker.DockerHelper
}

// NewDockerRuntime builds a DockerRuntime using ResolveDockerCommand to pick
// the binary; override wins if set.
func NewDockerRuntime(workspace *provider2.Workspace, override string) *DockerRuntime {
	return &DockerRuntime{
		helper: &docker.DockerHelper{
			DockerCommand: ResolveDockerCommand(workspace, override),
		},
	}
}

// DockerCommand returns the resolved binary path for callers that need the raw string.
func (r *DockerRuntime) DockerCommand() string { return r.helper.DockerCommand }

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

// ExecOneShotOptions configures a single non-interactive exec inside a
// workspace's running container.
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

// ExecOneShotResult is the outcome of an ExecOneShot call.
type ExecOneShotResult struct {
	ExitCode       int
	DurationMS     int64
	TimedOut       bool
	TimeoutSeconds int
	Clamped        bool
}

// ResolveTimeout picks the first positive of TimeoutSeconds,
// TimeoutSecondsDefault, fallbackDefault, then clamps by TimeoutSecondsMax.
// The bool is true when clamping applied.
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

// ExecOneShot runs opts.Command in the workspace's container, capturing
// stdout/stderr via the provided writers. Never reads stdin, never allocates a TTY.
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

// execOneShotWithRuntime is the testable seam: takes an already-resolved
// runtime so fakes can be injected without touching workspace lookup.
func execOneShotWithRuntime(
	ctx context.Context,
	runtime ContainerRuntime,
	req ExecRequest,
) (int, error) {
	return runtime.Exec(ctx, req)
}

// resolvedExecTarget wraps the resolve step's return values; a struct
// keeps it under revive's function-result-limit.
type resolvedExecTarget struct {
	runtime ContainerRuntime
	target  ContainerTarget
	workdir string
	envMap  map[string]string
}

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
