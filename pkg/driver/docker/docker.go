package docker

import (
	"context"
	"fmt"
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
) (driver.DockerDriver, error) {
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

	log.Debugf("using docker command: command=%s, runtime=%s", dockerCommand, rt.Name())
	return &dockerDriver{
		Docker: &docker.DockerHelper{
			DockerCommand: dockerCommand,
			Environment:   makeEnvironment(workspaceInfo.Agent.Docker.Env),
			ContainerID:   workspaceInfo.Workspace.Source.Container,
			Builder:       builder,
			Runtime:       rt,
		},
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
