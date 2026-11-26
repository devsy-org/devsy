package apple

import (
	"context"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/suite"
)

const runCall = "run"

type RunArgsValidationSuite struct {
	suite.Suite
	client *mockClient
	driver *appleDriver
}

func TestRunArgsValidationSuite(t *testing.T) {
	suite.Run(t, new(RunArgsValidationSuite))
}

func (s *RunArgsValidationSuite) SetupTest() {
	s.client = &mockClient{inspectErr: errors.New("image missing")}
	s.driver = &appleDriver{Apple: s.client}
}

func (s *RunArgsValidationSuite) TestRejectsBeforeAnyRuntimeCall() {
	s.ErrorContains(s.run("--rm", "busybox"), "the runtime would read it as the image")
	s.EqualError(
		s.run("--label", "sh.devsy.recovery=true"),
		`runArgs: label "sh.devsy.recovery" is reserved by Devsy; remove the user-supplied label`,
	)
	s.Empty(s.client.calls)
	s.Empty(s.client.ranArgs)
}

func (s *RunArgsValidationSuite) TestValidatesBeforeRecreateRemovesTheContainer() {
	err := s.driver.ValidateRunImageDevContainer(&driver.RunImageDevContainerParams{
		ParsedConfig: &config.DevContainerConfig{
			NonComposeBase: config.NonComposeBase{RunArgs: []string{"-l", "devsy.user=root"}},
		},
	})
	s.EqualError(
		err,
		`runArgs: label "devsy.user" is reserved by Devsy; remove the user-supplied label`,
	)
	s.Empty(s.client.calls)
}

func (s *RunArgsValidationSuite) TestOrdinaryRunArgsReachTheRuntime() {
	s.Require().NoError(s.run("--label", "team=a"))
	s.Equal([]string{"pull", runCall}, s.client.calls)
	s.Require().Len(s.client.ranArgs, 1)
	s.Contains(s.client.ranArgs[0], "team=a")
}

func (s *RunArgsValidationSuite) run(runArgs ...string) error {
	return s.driver.RunImageDevContainer(context.Background(), &driver.RunImageDevContainerParams{
		ParsedConfig: &config.DevContainerConfig{
			NonComposeBase: config.NonComposeBase{RunArgs: runArgs},
		},
		Options: &driver.RunOptions{Image: testImageRef},
	})
}
