package docker

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/suite"
)

const testDockerCmd = "docker"

type InvocationSuite struct {
	suite.Suite
}

func TestInvocationSuite(t *testing.T) {
	suite.Run(t, new(InvocationSuite))
}

func setCaseInsensitive(t *testing.T, v bool) {
	t.Helper()
	prev := envCaseInsensitive
	envCaseInsensitive = v
	t.Cleanup(func() { envCaseInsensitive = prev })
}

func (s *InvocationSuite) TestProviderEnvOverridesProcessEnv() {
	s.T().Setenv("DEVSY_TEST_ONLY_PROCESS", "process")
	s.T().Setenv("DOCKER_HOST", "unix:///process.sock")

	inv, err := NewDockerInvocation(&DockerHelper{
		DockerCommand: testDockerCmd,
		Environment:   []string{"DOCKER_HOST=ssh://provider", "EXTRA=a=b"},
	})
	s.Require().NoError(err)

	s.Equal(testDockerCmd, inv.DockerCmd)
	s.Equal("ssh://provider", inv.Env["DOCKER_HOST"])
	s.Equal("process", inv.Env["DEVSY_TEST_ONLY_PROCESS"])
	s.Equal("a=b", inv.Env["EXTRA"], "value keeps everything after the first '='")
}

func (s *InvocationSuite) TestSnapshotIsIndependentOfLaterChanges() {
	helper := &DockerHelper{DockerCommand: testDockerCmd, Environment: []string{"K=v1"}}
	inv, err := NewDockerInvocation(helper)
	s.Require().NoError(err)

	helper.Environment[0] = "K=v2"
	s.T().Setenv("K", "other")

	s.Equal("v1", inv.Env["K"])
}

func (s *InvocationSuite) TestWindowsProviderEnvReplacesDifferentlyCasedProcessKey() {
	setCaseInsensitive(s.T(), true)
	s.T().Setenv("DOCKER_HOST", "unix:///process.sock")

	inv, err := NewDockerInvocation(&DockerHelper{
		DockerCommand: testDockerCmd,
		Environment:   []string{"docker_host=ssh://provider"},
	})
	s.Require().NoError(err)

	count := 0
	for k := range inv.Env {
		if k == "DOCKER_HOST" || k == "docker_host" {
			count++
		}
	}
	s.Equal(1, count)
	v, ok := inv.Lookup("DOCKER_HOST")
	s.True(ok)
	s.Equal("ssh://provider", v)
}

func (s *InvocationSuite) TestLookupCaseSensitivity() {
	inv := &DockerInvocation{DockerCmd: testDockerCmd, Env: map[string]string{"Path": "x"}}

	setCaseInsensitive(s.T(), true)
	v, ok := inv.Lookup("PATH")
	s.True(ok)
	s.Equal("x", v)

	setCaseInsensitive(s.T(), false)
	_, ok = inv.Lookup("PATH")
	s.False(ok)
}

func (s *InvocationSuite) TestRejectsUnusableHelpers() {
	_, err := NewDockerInvocation(nil)
	s.Error(err)
	_, err = NewDockerInvocation(&DockerHelper{})
	s.Error(err)
	_, err = NewDockerInvocation(&DockerHelper{DockerCommand: testDockerCmd, Elevator: &Elevator{}})
	s.Error(err)
}

func (s *InvocationSuite) TestEnvironIsSortedKeyValuePairs() {
	inv := &DockerInvocation{DockerCmd: testDockerCmd, Env: map[string]string{"B": "2", "A": "1"}}
	s.Equal([]string{"A=1", "B=2"}, inv.Environ())
}

func (s *InvocationSuite) TestJSONRoundTrip() {
	inv := &DockerInvocation{DockerCmd: testDockerCmd, Env: map[string]string{"A": "1"}}
	data, err := json.Marshal(inv)
	s.Require().NoError(err)

	var got DockerInvocation
	s.Require().NoError(json.Unmarshal(data, &got))
	s.Equal(*inv, got)
}
