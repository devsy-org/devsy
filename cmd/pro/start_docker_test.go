package pro

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/suite"
)

type startDockerSuite struct {
	suite.Suite
}

func TestStartDockerSuite(t *testing.T) {
	suite.Run(t, new(startDockerSuite))
}

func (s *startDockerSuite) TestWrapCommandError() {
	// Nil error returns nil
	s.Nil(WrapCommandError([]byte("some output"), nil))

	// Non-nil error wraps output
	baseErr := errors.New("command failed")
	wrapped := WrapCommandError([]byte("sample stdout"), baseErr)
	s.Require().NotNil(wrapped)
	s.Contains(wrapped.Error(), "sample stdout")
	s.Contains(wrapped.Error(), "command failed")
	s.True(errors.Is(wrapped, baseErr))
}

func (s *startDockerSuite) TestContainerDetailsUnmarshal() {
	sampleJSON := []byte(`{
		"ID": "abc12345",
		"State": {
			"Status": "running",
			"StartedAt": "2026-10-05T12:00:00Z"
		},
		"NetworkSettings": {
			"ports": {
				"10443/tcp": [
					{
						"HostIp": "127.0.0.1",
						"HostPort": "9898"
					}
				]
			}
		},
		"Config": {
			"Image": "ghcr.io/devsy-org/devsy-pro:latest",
			"User": "root"
		}
	}`)

	var details ContainerDetails
	err := json.Unmarshal(sampleJSON, &details)
	s.Require().NoError(err)
	s.Equal("abc12345", details.ID)
	s.Equal(devcconfig.ContainerStatus("running"), details.State.Status)
	s.Equal("2026-10-05T12:00:00Z", details.State.StartedAt)
	s.Require().Contains(details.NetworkSettings.Ports, "10443/tcp")
	ports := details.NetworkSettings.Ports["10443/tcp"]
	s.Require().Len(ports, 1)
	s.Equal("127.0.0.1", ports[0].HostIP)
	s.Equal("9898", ports[0].HostPort)
	s.Equal("ghcr.io/devsy-org/devsy-pro:latest", details.Config.Image)
	s.Equal("root", details.Config.User)
}

func (s *startDockerSuite) TestGetMachineUID() {
	uid := getMachineUID()
	s.NotEmpty(uid)
	// Output is hex representation of sha256 HMAC (64 characters)
	s.Len(uid, 64)
}

func (s *startDockerSuite) TestResetExistingContainerNoOp() {
	cmd := &StartCmd{
		Reset:   false,
		Upgrade: false,
	}

	res, err := cmd.resetExistingContainer(context.Background(), "existing-container-id")
	s.NoError(err)
	s.Equal("existing-container-id", res)

	resEmpty, err := cmd.resetExistingContainer(context.Background(), "")
	s.NoError(err)
	s.Equal("", resEmpty)
}

func (s *startDockerSuite) TestResolveRunningContainerEmpty() {
	cmd := &StartCmd{}
	res, err := cmd.resolveRunningContainer(context.Background(), []string{}, true)
	s.NoError(err)
	s.Equal("", res)
}
