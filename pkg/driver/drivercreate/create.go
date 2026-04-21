package drivercreate

import (
	"fmt"

	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/driver/custom"
	"github.com/devsy-org/devsy/pkg/driver/docker"
	"github.com/devsy-org/devsy/pkg/driver/kubernetes"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

func NewDriver(workspaceInfo *provider2.AgentWorkspaceInfo) (driver.Driver, error) {
	driver := workspaceInfo.Agent.Driver
	switch driver {
	case "", provider2.DockerDriver:
		return docker.NewDockerDriver(workspaceInfo)
	case provider2.CustomDriver:
		return custom.NewCustomDriver(workspaceInfo), nil
	case provider2.KubernetesDriver:
		return kubernetes.NewKubernetesDriver(workspaceInfo)
	}

	return nil, fmt.Errorf("unrecognized driver '%s', possible values are %s, %s or %s",
		driver, provider2.DockerDriver, provider2.CustomDriver, provider2.KubernetesDriver)
}
