package docker

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/devsy-org/devsy/pkg/compose"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
)

const (
	// dockerExec is the docker subcommand for running a command in a container.
	dockerExec = "exec"
	// rootUser is the conventional root account name/uid-0 owner.
	rootUser = "root"
)

func makeEnvironment(env map[string]string) []string {
	if env == nil {
		return nil
	}

	ret := config.ObjectToList(env)
	if len(env) > 0 {
		log.Debugf("using docker environment variables: variables=%v", ret)
	}

	return ret
}

func NewDockerDriver(
	workspaceInfo *provider.AgentWorkspaceInfo,
) (driver.ImageDriver, error) {
	dockerCommand := "docker"
	if workspaceInfo.Agent.Docker.Path != "" {
		dockerCommand = workspaceInfo.Agent.Docker.Path
	}

	var builder docker.DockerBuilder
	var err error
	builder, err = docker.DockerBuilderFromString(workspaceInfo.Agent.Docker.Builder)
	if err != nil {
		return nil, err
	}

	var rt docker.ContainerRuntime
	if workspaceInfo.Agent.Docker.Runtime != "" {
		var err error
		rt, err = docker.RuntimeFromName(workspaceInfo.Agent.Docker.Runtime)
		if err != nil {
			return nil, fmt.Errorf("invalid runtime config: %w", err)
		}
	} else {
		rt = docker.DetectRuntime(dockerCommand)
	}

	elevator, err := docker.ElevatorFromName(workspaceInfo.Agent.Docker.Elevation)
	if err != nil {
		return nil, fmt.Errorf("invalid elevation config: %w", err)
	}

	log.Debugf("using docker command: command=%s, runtime=%s", dockerCommand, rt.Name())
	dockerHelper := &docker.DockerHelper{
		DockerCommand: dockerCommand,
		Environment:   makeEnvironment(workspaceInfo.Agent.Docker.Env),
		ContainerID:   workspaceInfo.Workspace.Source.Container,
		Builder:       builder,
		Runtime:       rt,
		Elevator:      elevator,
	}

	// Authenticate before any command runs, keeping the prompt out of the short
	// per-command probe timeouts that would otherwise kill it.
	if err := dockerHelper.EnsureElevated(); err != nil {
		return nil, err
	}

	return &dockerDriver{
		Docker:                     dockerHelper,
		IDLabels:                   workspaceInfo.CLIOptions.IDLabels,
		UpdateRemoteUserUIDDefault: workspaceInfo.CLIOptions.UpdateRemoteUserUIDDefault,
	}, nil
}

type dockerDriver struct {
	Docker                     *docker.DockerHelper
	Compose                    *compose.ComposeHelper
	IDLabels                   []string
	UpdateRemoteUserUIDDefault string
}

// The docker driver supports the full image, compose, docker-helper, and
// snapshot-commit capabilities.
var (
	_ driver.ImageDriver           = (*dockerDriver)(nil)
	_ driver.ComposeDriver         = (*dockerDriver)(nil)
	_ driver.DockerHelperProvider  = (*dockerDriver)(nil)
	_ driver.SnapshotCapableDriver = (*dockerDriver)(nil)
	_ driver.Preflighter           = (*dockerDriver)(nil)
)

// Preflight checks the runtime binary is installed and its daemon reachable,
// starting a stopped Podman machine unless auto-start is disabled.
func (d *dockerDriver) Preflight(ctx context.Context, opts driver.PreflightOptions) error {
	return runPreflight(ctx, opts, dockerProbe{
		command:       d.Docker.DockerCommand,
		runtime:       d.Docker.GetRuntime().Name(),
		lookPath:      exec.LookPath,
		ping:          d.Docker.Ping,
		start:         d.Docker.StartPodmanMachine,
		machineExists: d.Docker.PodmanMachineExists,
	})
}

// dockerProbe bundles the operations runPreflight depends on so the branching
// can be exercised with fakes, without abstracting the whole DockerHelper.
type dockerProbe struct {
	command  string
	runtime  docker.RuntimeName
	lookPath func(string) (string, error)
	ping     func(context.Context) error
	start    func(context.Context) error
	// machineExists reports whether a Podman machine exists to start. It is
	// consulted only after a failed ping, so the common reachable case pays no
	// extra cost. A nil machineExists assumes a machine exists (preserves the
	// unconditional start behavior for callers that don't supply it).
	machineExists func(context.Context) bool
}

func runPreflight(ctx context.Context, opts driver.PreflightOptions, p dockerProbe) error {
	runtimeName := string(p.runtime)

	if _, err := p.lookPath(p.command); err != nil {
		return &driver.PreflightError{
			Provider: runtimeName,
			Err:      fmt.Errorf("%s is not installed or not on PATH: %w", p.command, err),
		}
	}

	err := p.ping(ctx)
	if err == nil {
		return nil
	}

	if p.runtime == docker.RuntimePodman && !opts.DisableAutoStart && (p.machineExists == nil || p.machineExists(ctx)) {
		log.Infof(
			"Podman machine is not running, attempting to start it (this may take a while)...",
		)
		if startErr := p.start(ctx); startErr != nil {
			log.Warnf("failed to start Podman machine: %v", startErr)
		} else if err = p.ping(ctx); err == nil {
			return nil
		}
	}

	return &driver.PreflightError{Provider: runtimeName, Err: err}
}

func (d *dockerDriver) TargetArchitecture(ctx context.Context, workspaceId string) (string, error) {
	return runtime.GOARCH, nil
}

func (d *dockerDriver) ComposeHelper() (*compose.ComposeHelper, error) {
	if d.Compose != nil {
		return d.Compose, nil
	}

	var err error
	d.Compose, err = compose.NewComposeHelper(d.Docker)
	return d.Compose, err
}

func (d *dockerDriver) DockerHelper() (*docker.DockerHelper, error) {
	if d.Docker == nil {
		return nil, fmt.Errorf("no docker helper available")
	}

	return d.Docker, nil
}

func (d *dockerDriver) FindDevContainer(
	ctx context.Context,
	workspaceId string,
) (*config.ContainerDetails, error) {
	var containerDetails *config.ContainerDetails
	var err error
	if d.Docker.ContainerID != "" {
		containerDetails, err = d.Docker.FindContainerByID(ctx, []string{d.Docker.ContainerID})
	} else {
		containerDetails, err = d.Docker.FindDevContainer(
			ctx,
			config.GetIDLabels(workspaceId, d.IDLabels),
		)
	}
	if err != nil {
		return nil, err
	} else if containerDetails == nil {
		return nil, nil
	}

	if containerDetails.Config.User != "" {
		if containerDetails.Config.Labels == nil {
			containerDetails.Config.Labels = map[string]string{}
		}
		if containerDetails.Config.Labels[config.UserLabel] == "" {
			containerDetails.Config.Labels[config.UserLabel] = containerDetails.Config.User
		}
	}

	return containerDetails, nil
}
