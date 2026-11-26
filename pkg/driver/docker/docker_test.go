package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/stretchr/testify/suite"
)

const (
	testSeccompUnconfined   = "seccomp=unconfined"
	testSecurityOptFlag     = "--security-opt"
	testBindMount           = "type=bind,src=/a,dst=/b"
	testUpdateUIDDefaultOff = "off"
	testUpdateUIDDefaultOn  = "on"
	testRemoteUser          = "vscode"
	testRunArg              = "run"
	testDockerCmd           = "docker"
	testContainerUser       = "container"
)

type DockerDriverTestSuite struct {
	suite.Suite
	driver *dockerDriver
}

func TestDockerDriverSuite(t *testing.T) {
	suite.Run(t, new(DockerDriverTestSuite))
}

func (s *DockerDriverTestSuite) SetupTest() {
	s.driver = &dockerDriver{}
}

func (s *DockerDriverTestSuite) TestEnsureUserLabel_NilDetails() {
	s.NotPanics(func() {
		ensureUserLabel(nil)
	})
}

func (s *DockerDriverTestSuite) TestEnsureUserLabel_EmptyUser() {
	details := &config.ContainerDetails{
		ID: "c1",
		Config: config.ContainerDetailsConfig{
			User:   "",
			Labels: map[string]string{config.UserLabel: "existing"},
		},
	}
	ensureUserLabel(details)
	s.Equal("existing", details.Config.Labels[config.UserLabel])

	detailsNilLabels := &config.ContainerDetails{
		ID: "c1",
		Config: config.ContainerDetailsConfig{
			User:   "",
			Labels: nil,
		},
	}
	ensureUserLabel(detailsNilLabels)
	s.Nil(detailsNilLabels.Config.Labels)
}

func (s *DockerDriverTestSuite) TestEnsureUserLabel_InjectsWhenMissing() {
	details := &config.ContainerDetails{
		ID: "c1",
		Config: config.ContainerDetailsConfig{
			User:   "1000",
			Labels: nil,
		},
	}
	ensureUserLabel(details)
	s.NotNil(details.Config.Labels)
	s.Equal("1000", details.Config.Labels[config.UserLabel])
}

func (s *DockerDriverTestSuite) TestEnsureUserLabel_PreservesExistingLabel() {
	details := &config.ContainerDetails{
		ID: "c1",
		Config: config.ContainerDetailsConfig{
			User: "1000",
			Labels: map[string]string{
				config.UserLabel: "custom-user",
			},
		},
	}
	ensureUserLabel(details)
	s.Equal("custom-user", details.Config.Labels[config.UserLabel])
}

func (s *DockerDriverTestSuite) TestFindDevContainer_UsesContainerIDWhenPinned() {
	script := `#!/bin/sh
case "$1" in
  inspect)
    echo '[{"ID":"pinned-123","Config":{"User":"node"}}]'
    ;;
  *)
    exit 1
    ;;
esac
`
	dir := s.T().TempDir()
	bin := filepath.Join(dir, "docker-fake")
	s.Require().NoError(os.WriteFile(bin, []byte(script), 0o755)) //nolint:gosec

	d := &dockerDriver{
		Docker: &docker.DockerHelper{
			DockerCommand: bin,
			ContainerID:   "pinned-123",
		},
	}

	details, err := d.FindDevContainer(context.Background(), "my-workspace")
	s.Require().NoError(err)
	s.Require().NotNil(details)
	s.Equal("pinned-123", details.ID)
	s.Equal("node", details.Config.Labels[config.UserLabel])
}

func (s *DockerDriverTestSuite) TestFindDevContainer_FindByWorkspaceLabels() {
	script := `#!/bin/sh
case "$1" in
  ps)
    echo "c-discovered"
    ;;
  inspect)
    echo '[{"ID":"c-discovered","Config":{"User":"vscode","Labels":{"devsy.user":"override"}}}]'
    ;;
  *)
    exit 1
    ;;
esac
`
	dir := s.T().TempDir()
	bin := filepath.Join(dir, "docker-fake")
	s.Require().NoError(os.WriteFile(bin, []byte(script), 0o755)) //nolint:gosec

	d := &dockerDriver{
		Docker: &docker.DockerHelper{
			DockerCommand: bin,
		},
		IDLabels: []string{"custom.label/workspace"},
	}

	details, err := d.FindDevContainer(context.Background(), "my-workspace")
	s.Require().NoError(err)
	s.Require().NotNil(details)
	s.Equal("c-discovered", details.ID)
	s.Equal("override", details.Config.Labels[config.UserLabel])
}

func (s *DockerDriverTestSuite) TestFindDevContainer_NotFoundReturnsNil() {
	script := `#!/bin/sh
case "$1" in
  ps)
    # Return empty - no container found
    ;;
  *)
    exit 1
    ;;
esac
`
	dir := s.T().TempDir()
	bin := filepath.Join(dir, "docker-fake")
	s.Require().NoError(os.WriteFile(bin, []byte(script), 0o755)) //nolint:gosec

	d := &dockerDriver{
		Docker: &docker.DockerHelper{
			DockerCommand: bin,
		},
	}

	details, err := d.FindDevContainer(context.Background(), "nonexistent")
	s.Require().NoError(err)
	s.Nil(details)
}

func (s *DockerDriverTestSuite) TestFindDevContainer_ErrorPropagated() {
	script := `#!/bin/sh
exit 1
`
	dir := s.T().TempDir()
	bin := filepath.Join(dir, "docker-fake")
	s.Require().NoError(os.WriteFile(bin, []byte(script), 0o755)) //nolint:gosec

	d := &dockerDriver{
		Docker: &docker.DockerHelper{
			DockerCommand: bin,
			ContainerID:   "failing-id",
		},
	}

	details, err := d.FindDevContainer(context.Background(), "ws")
	s.Require().Error(err)
	s.Nil(details)
}
