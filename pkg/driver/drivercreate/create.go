package drivercreate

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/driver/apple"
	"github.com/devsy-org/devsy/pkg/driver/custom"
	"github.com/devsy-org/devsy/pkg/driver/docker"
	"github.com/devsy-org/devsy/pkg/driver/kubernetes"
	"github.com/devsy-org/devsy/pkg/driver/microsandbox"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

type Bundle struct {
	Runtime driver.Driver
	Images  driver.ImageBackend
}

func New(ctx context.Context, workspaceInfo *provider2.AgentWorkspaceInfo) (*Bundle, error) {
	runtimeDriver, err := newRuntime(ctx, workspaceInfo)
	if err != nil {
		return nil, err
	}
	bundle := &Bundle{Runtime: runtimeDriver}
	if workspaceInfo.Agent.Driver == provider2.MicrosandboxDriver {
		// Preserve registry inspection and defer Docker setup until a build or publish.
		bundle.Images = &microsandboxImages{
			ImageInspector: runtimeDriver.(driver.ImageInspector),
			workspaceInfo:  workspaceInfo,
		}
	} else if images, ok := runtimeDriver.(driver.ImageBackend); ok {
		bundle.Images = images
	}
	return bundle, nil
}

// NewDriver retains the runtime-only constructor for existing callers.
func NewDriver(
	ctx context.Context,
	workspaceInfo *provider2.AgentWorkspaceInfo,
) (driver.Driver, error) {
	bundle, err := New(ctx, workspaceInfo)
	if err != nil {
		return nil, err
	}
	return bundle.Runtime, nil
}

func newRuntime(
	ctx context.Context,
	workspaceInfo *provider2.AgentWorkspaceInfo,
) (driver.Driver, error) {
	driver := workspaceInfo.Agent.Driver
	switch driver {
	case "", provider2.DockerDriver:
		return docker.NewDockerDriver(workspaceInfo)
	case provider2.CustomDriver:
		return custom.NewCustomDriver(workspaceInfo), nil
	case provider2.KubernetesDriver:
		return kubernetes.NewKubernetesDriver(workspaceInfo)
	case provider2.AppleDriver:
		return apple.NewAppleDriver(ctx, workspaceInfo)
	case provider2.MicrosandboxDriver:
		return microsandbox.NewMicrosandboxDriver(ctx, workspaceInfo)
	}

	return nil, fmt.Errorf(
		"unrecognized driver %q, possible values are %s, %s, %s, %s or %s",
		driver, provider2.DockerDriver, provider2.CustomDriver, provider2.KubernetesDriver,
		provider2.AppleDriver, provider2.MicrosandboxDriver)
}
