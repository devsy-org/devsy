// Package apple implements a Devsy driver for Apple's `container` CLI, which runs
// Linux containers as lightweight VMs on Apple silicon (macOS 26+). It implements
// driver.ImageDriver; compose and docker-helper capabilities are intentionally
// not implemented (callers detect their absence by type assertion).
package apple

import (
	"context"
	"fmt"
	"runtime"

	"github.com/devsy-org/devsy/pkg/apple"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
)

const (
	appleExec      = "exec"
	defaultCommand = "container"
	statusRunning  = "running"
)

type appleDriver struct {
	Apple       appleClient
	IDLabels    []string
	Rosetta     bool
	command     string // binary path, retained for logging
	containerID string // set when the workspace source is an existing container
}

var _ driver.ImageDriver = (*appleDriver)(nil)

// NewAppleDriver verifies the host is supported and the container system service
// is running (ctx bounds the potentially-slow `container system start`).
func NewAppleDriver(
	ctx context.Context,
	workspaceInfo *provider.AgentWorkspaceInfo,
) (driver.ImageDriver, error) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return nil, fmt.Errorf(
			"the apple provider requires an Apple silicon Mac (found %s/%s)",
			runtime.GOOS, runtime.GOARCH,
		)
	}

	command := defaultCommand
	if workspaceInfo.Agent.Apple.Path != "" {
		command = workspaceInfo.Agent.Apple.Path
	}

	helper := &apple.AppleHelper{
		Command:     command,
		Environment: makeEnvironment(workspaceInfo.Agent.Apple.Env),
	}

	if err := helper.EnsureSystemRunning(ctx); err != nil {
		return nil, err
	}

	rosetta, err := workspaceInfo.Agent.Apple.Rosetta.Bool()
	if err != nil {
		log.Warnf(
			"invalid rosetta value %q, defaulting to false: %v",
			workspaceInfo.Agent.Apple.Rosetta, err,
		)
		rosetta = false
	}

	log.Debugf("using apple container command: command=%s, rosetta=%t", command, rosetta)
	return &appleDriver{
		Apple:       helper,
		IDLabels:    workspaceInfo.CLIOptions.IDLabels,
		Rosetta:     rosetta,
		command:     command,
		containerID: workspaceInfo.Workspace.Source.Container,
	}, nil
}

func makeEnvironment(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	return config.ObjectToList(env)
}

func (d *appleDriver) TargetArchitecture(ctx context.Context, workspaceID string) (string, error) {
	return runtime.GOARCH, nil
}

func (d *appleDriver) FindDevContainer(
	ctx context.Context,
	workspaceID string,
) (*config.ContainerDetails, error) {
	var containerDetails *config.ContainerDetails
	var err error
	if d.containerID != "" {
		containerDetails, err = d.Apple.FindContainerByID(ctx, []string{d.containerID})
	} else {
		containerDetails, err = d.Apple.FindDevContainer(
			ctx,
			config.GetIDLabels(workspaceID, d.IDLabels),
		)
	}
	if err != nil || containerDetails == nil {
		return nil, err
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
