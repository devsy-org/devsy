package docker

import (
	"context"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
)

func (s *DockerDriverTestSuite) TestRunImageDevContainerRejectsRunArgsBeforeRuntimeCall() {
	missing := filepath.Join(s.T().TempDir(), "missing-runtime")
	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: missing}}
	run := func(runArgs ...string) error {
		return d.RunImageDevContainer(context.Background(), &driver.RunImageDevContainerParams{
			ParsedConfig: &config.DevContainerConfig{
				NonComposeBase: config.NonComposeBase{RunArgs: runArgs},
			},
			Options: &driver.RunOptions{Image: "alpine:3.22"},
		})
	}

	s.EqualError(
		run("--privileged", "busybox"),
		"runArgs: item 2: not an option or option value; the runtime would read it as the image",
	)
	s.EqualError(
		run("-l=devsy.user=root"),
		`runArgs: label "devsy.user" is reserved by Devsy; remove the user-supplied label`,
	)
	s.ErrorContains(run("--label", "team=a"), "missing-runtime")
}

func (s *DockerDriverTestSuite) TestValidateRunImageDevContainerChecksRunArgs() {
	err := (&dockerDriver{}).ValidateRunImageDevContainer(&driver.RunImageDevContainerParams{
		ParsedConfig: &config.DevContainerConfig{
			NonComposeBase: config.NonComposeBase{RunArgs: []string{"--tty", "false"}},
		},
	})
	s.EqualError(
		err,
		"runArgs: item 2: not an option or option value; the runtime would read it as the image",
	)
}
