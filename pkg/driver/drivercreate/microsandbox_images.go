package drivercreate

import (
	"context"
	"fmt"
	"sync"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/driver/docker"
	"github.com/devsy-org/devsy/pkg/provider"
)

type microsandboxImages struct {
	driver.ImageInspector
	workspaceInfo *provider.AgentWorkspaceInfo
	mu            sync.Mutex
	backend       driver.ImageBackend
}

func (m *microsandboxImages) BuildDevContainer(
	ctx context.Context,
	req driver.BuildRequest,
) (*config.BuildInfo, error) {
	backend, err := m.dockerBackend()
	if err != nil {
		return nil, err
	}
	return backend.BuildDevContainer(ctx, req)
}

func (m *microsandboxImages) PushDevContainer(ctx context.Context, image string) error {
	backend, err := m.dockerBackend()
	if err != nil {
		return err
	}
	return backend.PushDevContainer(ctx, image)
}

func (m *microsandboxImages) TagDevContainer(ctx context.Context, image, tag string) error {
	backend, err := m.dockerBackend()
	if err != nil {
		return err
	}
	return backend.TagDevContainer(ctx, image, tag)
}

func (m *microsandboxImages) dockerBackend() (driver.ImageBackend, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.backend != nil {
		return m.backend, nil
	}
	backend, err := docker.NewDockerDriver(m.workspaceInfo)
	if err != nil {
		return nil, fmt.Errorf("microsandbox needs docker to build this devcontainer: %w", err)
	}
	m.backend = backend
	return backend, nil
}
