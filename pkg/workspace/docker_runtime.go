package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

// DockerRuntime is the production ContainerRuntime that shells out to a
// docker-compatible binary.
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
