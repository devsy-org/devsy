package apple

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/apple"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/suite"
)

const (
	nativeIntendedImage   = "alpine:3.22"
	nativePositionalImage = "busybox:1"
)

type NativeRunArgsSuite struct {
	suite.Suite
	helper *apple.AppleHelper
}

func TestNativeRunArgsSuite(t *testing.T) {
	suite.Run(t, new(NativeRunArgsSuite))
}

func (s *NativeRunArgsSuite) SetupTest() {
	if os.Getenv("DEVSY_APPLE_E2E") != "1" {
		s.T().Skip("set DEVSY_APPLE_E2E=1 to run against a real container CLI")
	}
	s.helper = &apple.AppleHelper{Command: "container"}
}

func (s *NativeRunArgsSuite) TestPositionalImageIsRejected() {
	name := s.containerName("positional")
	err := s.runDevContainer(name, "--name", name, "--read-only", nativePositionalImage, "echo")
	s.T().Logf("RunImageDevContainer error: %v", err)
	s.assertNoContainer(name)
	s.Require().ErrorContains(err, "the runtime would read it as the image")
}

func (s *NativeRunArgsSuite) TestReservedLabelIsRejected() {
	name := s.containerName("reserved")
	err := s.runDevContainer(
		name, "--name", name, "--label", "sh.devsy.recovery=fixture-private-value",
	)
	s.T().Logf("RunImageDevContainer error: %v", err)
	s.assertNoContainer(name)
	s.Require().ErrorContains(err, `label "sh.devsy.recovery" is reserved by Devsy`)
	s.NotContains(err.Error(), "fixture-private-value")
}

func (s *NativeRunArgsSuite) TestOrdinaryLabelStarts() {
	name := s.containerName("ordinary")
	s.Require().NoError(s.runDevContainer(name, "--name", name, "--label", "team=a"))
	s.Require().NoError(s.helper.WaitContainerRunning(context.Background(), name))

	details, err := s.helper.InspectContainers(context.Background(), []string{name})
	s.Require().NoError(err)
	s.Require().Len(details, 1)
	s.Equal("a", details[0].Config.Labels["team"])
	s.Equal("root", details[0].Config.Labels[config.UserLabel])
}

func (s *NativeRunArgsSuite) containerName(kind string) string {
	name := fmt.Sprintf("devsy-runargs-%s-%d", kind, time.Now().UnixNano())
	s.T().Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = s.helper.Stop(ctx, name)
		_ = s.helper.Remove(ctx, name)
	})
	return name
}

func (s *NativeRunArgsSuite) runDevContainer(name string, runArgs ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return (&appleDriver{Apple: s.helper}).RunImageDevContainer(
		ctx,
		&driver.RunImageDevContainerParams{
			WorkspaceID: name,
			ParsedConfig: &config.DevContainerConfig{
				NonComposeBase: config.NonComposeBase{RunArgs: runArgs},
			},
			Options: &driver.RunOptions{
				Image:      nativeIntendedImage,
				Labels:     []string{config.UserLabel + "=root"},
				Entrypoint: "/bin/sh",
				Cmd:        []string{"-c", "sleep 120"},
			},
		},
	)
}

func (s *NativeRunArgsSuite) assertNoContainer(name string) {
	details, err := s.helper.InspectContainers(context.Background(), []string{name})
	if err != nil || len(details) == 0 {
		s.T().Logf("%s: no container", name)
		return
	}
	s.Failf(
		"container was created",
		"%s: status=%s image=%s labels=%v",
		name,
		details[0].State.Status,
		details[0].Config.LegacyImage,
		details[0].Config.Labels,
	)
}
