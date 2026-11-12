package docker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/suite"
)

type NativeReservedLabelsSuite struct{ suite.Suite }

const (
	testLabelFlag   = "--label"
	testAlpineImage = "alpine:3.22"
)

func TestNativeReservedLabelsSuite(t *testing.T) { suite.Run(t, new(NativeReservedLabelsSuite)) }

func (s *NativeReservedLabelsSuite) TestPositionalImage() {
	if os.Getenv("DEVSY_REAL_DOCKER_TEST") != "1" {
		s.T().Skip("requires an explicitly enabled Docker runtime")
	}
	name := fmt.Sprintf("devsy-label-proof-%d", time.Now().UnixNano())
	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: testDockerCmd}}
	s.T().
		Cleanup(func() { _ = d.Docker.Run(context.Background(), []string{"rm", "-f", name}, docker.Streams{}) })
	err := d.RunImageDevContainer(context.Background(), &driver.RunImageDevContainerParams{
		WorkspaceID: "label-proof",
		ParsedConfig: &config.DevContainerConfig{
			NonComposeBase: config.NonComposeBase{RunArgs: []string{
				"--name", name, testLabelFlag, "sh.devsy.recovery=fixture-private-value",
				"--entrypoint", testShellPath, testAlpineImage, "-c", "sleep 120",
			}},
		},
		Options: &driver.RunOptions{Image: testAlpineImage, Labels: []string{"devsy.user=root"}},
	})
	s.Require().ErrorContains(err, `label "sh.devsy.recovery" does not match the value Devsy set`)
	s.NotContains(err.Error(), "fixture-private-value")
	s.T().Logf("after: %s", err)
	out, err := exec.Command(testDockerCmd, "ps", "-a", "--format", "{{.Names}}").Output()
	s.Require().NoError(err)
	s.NotContains(strings.Split(strings.TrimSpace(string(out)), "\n"), name)
	s.T().Log("docker ps -a: no container")
}

func (s *NativeReservedLabelsSuite) TestOrdinaryLabelStarts() {
	if os.Getenv("DEVSY_REAL_DOCKER_TEST") != "1" {
		s.T().Skip("requires an explicitly enabled Docker runtime")
	}
	name := fmt.Sprintf("devsy-label-control-%d", time.Now().UnixNano())
	d := &dockerDriver{
		Docker:                     &docker.DockerHelper{DockerCommand: testDockerCmd},
		UpdateRemoteUserUIDDefault: "off",
	}
	s.T().
		Cleanup(func() { _ = d.Docker.Run(context.Background(), []string{"rm", "-f", name}, docker.Streams{}) })
	err := d.RunImageDevContainer(context.Background(), &driver.RunImageDevContainerParams{
		WorkspaceID: "label-control",
		ParsedConfig: &config.DevContainerConfig{NonComposeBase: config.NonComposeBase{
			RunArgs: []string{"--name", name, testLabelFlag, "team=a"},
		}},
		Options: &driver.RunOptions{
			Image: testAlpineImage, Labels: []string{"devsy.user=root"},
			Entrypoint: testShellPath, Cmd: []string{"-c", "sleep 120"},
		},
	})
	s.Require().NoError(err)
	details, err := d.Docker.InspectContainers(context.Background(), []string{name})
	s.Require().NoError(err)
	s.Require().Len(details, 1)
	s.Equal(config.ContainerStatusRunning, details[0].State.Status)
	s.Equal("a", details[0].Config.Labels["team"])
}
