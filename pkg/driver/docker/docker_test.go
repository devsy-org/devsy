package docker

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

const (
	testSeccompUnconfined   = "seccomp=unconfined"
	testSecurityOptFlag     = "--security-opt"
	testBindMount           = "type=bind,src=/a,dst=/b"
	testUpdateUIDDefaultOff = "off"
	testUpdateUIDDefaultOn  = "on"
	testOSLinux             = "linux"
	testRemoteUser          = "vscode"
	testRunArg              = "run"
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
