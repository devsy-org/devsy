// Package apple implements a Devsy driver for Apple's `container` CLI, which runs
// Linux containers as lightweight VMs on Apple silicon (macOS 26+). It implements
// driver.ImageDriver; compose, docker-helper, and snapshot-commit capabilities
// are intentionally not implemented (callers detect their absence by type
// assertion).
package apple

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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
)

type appleDriver struct {
	Apple       appleClient
	IDLabels    []string
	Rosetta     bool
	command     string // binary path, retained for logging
	containerID string // set when the workspace source is an existing container
}

var (
	_ driver.ImageDriver = (*appleDriver)(nil)
	_ driver.Preflighter = (*appleDriver)(nil)
)

// NewAppleDriver verifies the host is supported and constructs the driver;
// Preflight validates the container system service.
func NewAppleDriver(
	_ context.Context,
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

// Preflight checks the `container` binary is installed and the system service
// running, starting it unless auto-start is disabled.
func (d *appleDriver) Preflight(ctx context.Context, opts driver.PreflightOptions) error {
	if _, err := exec.LookPath(d.command); err != nil {
		return &driver.PreflightError{
			Provider: provider.AppleDriver,
			Err:      fmt.Errorf("%s is not installed or not on PATH: %w", d.command, err),
		}
	}

	if opts.DisableAutoStart {
		if !d.Apple.SystemRunning(ctx) {
			return &driver.PreflightError{
				Provider: provider.AppleDriver,
				Err: errors.New(
					"container system service is not running (run `container system start`)",
				),
			}
		}
		return nil
	}

	if err := d.Apple.EnsureSystemRunning(ctx); err != nil {
		return &driver.PreflightError{Provider: provider.AppleDriver, Err: err}
	}
	return nil
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
