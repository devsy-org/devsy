package drivercreate

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/driver/apple"
	"github.com/devsy-org/devsy/pkg/driver/custom"
	"github.com/devsy-org/devsy/pkg/driver/docker"
	"github.com/devsy-org/devsy/pkg/driver/kubernetes"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

func NewDriver(
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
	}

	return nil, fmt.Errorf("unrecognized driver %q, possible values are %s, %s, %s or %s",
		driver, provider2.DockerDriver, provider2.CustomDriver, provider2.KubernetesDriver,
		provider2.AppleDriver)
}
