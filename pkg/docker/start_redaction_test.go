package docker

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/stretchr/testify/suite"
	"go.uber.org/zap/zapcore"
)

type StartRedactionSuite struct{ suite.Suite }

func TestStartRedactionSuite(t *testing.T) { suite.Run(t, new(StartRedactionSuite)) }

func (s *StartRedactionSuite) TestStartFailure() {
	observed := log.InitTestObserved(s.T(), zapcore.ErrorLevel)
	path := writeScript(s.T(), s.T().TempDir(), "engine", `#!/bin/sh
printf '%s\n' "$FIXTURE_TOKEN" "$ORDINARY"
if [ "$1" = start ]; then exit 1; fi
`)
	h := &DockerHelper{DockerCommand: path, Environment: []string{
		"FIXTURE_TOKEN=fixture-private-value", "ORDINARY=container-private-value",
	}}
	err := h.StartContainerWithEnvironment(
		context.Background(),
		"container",
		[]string{"ORDINARY=container-private-value"},
	)
	s.Require().Error(err)
	s.NotContains(err.Error(), "fixture-private-value")
	s.Require().NotEmpty(observed.All())
	s.NotContains(observed.All()[0].Message, "fixture-private-value")
	s.NotContains(err.Error(), "container-private-value")
	s.NotContains(observed.All()[0].Message, "container-private-value")
	s.Contains(err.Error(), "***")
	s.Contains(observed.All()[0].Message, "***")
}

func (s *StartRedactionSuite) TestInspectCreatedContainer() {
	path := writeScript(s.T(), s.T().TempDir(), "engine", `#!/bin/sh
printf '%s\n' '[{"Id":"created-id","Config":{"Labels":{"team":"a"},"Env":["TOKEN=fixture-private-value"]}}]'
`)
	h := &DockerHelper{DockerCommand: path}
	labels, environment, err := h.InspectCreatedContainer(context.Background(), "created-id")
	s.Require().NoError(err)
	s.Equal(map[string]string{"team": "a"}, labels)
	s.Equal([]string{"TOKEN=fixture-private-value"}, environment)
}
