package workspace

import (
	"errors"
	"io"
	"os"
	"testing"

	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
)

type logsEnvironmentSuite struct{ suite.Suite }

func TestLogsEnvironment(t *testing.T) { suite.Run(t, new(logsEnvironmentSuite)) }

func (s *logsEnvironmentSuite) TestLocalInjection() {
	s.T().Setenv(config.EnvHome, s.T().TempDir())
	s.T().Setenv("MSB_PATH", "ambient-runtime")
	s.T().Setenv("MSB_LIBKRUNFW_PATH", "")
	s.T().Setenv("MSB_HOME", "")
	for _, tc := range []struct{ name, driver, want string }{
		{name: "external", driver: provider.ExternalDriver, want: "configured runtime|configured firmware|workspace home"},
		{name: "docker", driver: provider.DockerDriver, want: "ambient-runtime||"},
	} {
		s.Run(tc.name, func() {
			c := newLogsEnvironmentClient(tc.driver)
			reader, writer, err := os.Pipe()
			s.Require().NoError(err)
			defer func() { s.NoError(reader.Close()) }()
			defer func() { _ = writer.Close() }()
			err = injectLogsAgent(s.T().Context(), injectLogsAgentParams{
				client: c, stdout: writer,
				sshServerCmd: `printf '%s|%s|%s' "$MSB_PATH" "$MSB_LIBKRUNFW_PATH" "$MSB_HOME"`,
			})
			s.Require().NoError(err)
			s.Require().NoError(writer.Close())
			output, err := io.ReadAll(reader)
			s.Require().NoError(err)
			s.Equal(tc.want, string(output))
			s.Equal("ambient-runtime", os.Getenv("MSB_PATH"))
		})
	}
}

func (s *logsEnvironmentSuite) TestResolutionError() {
	c := newLogsEnvironmentClient(provider.ExternalDriver)
	c.infoErr = errors.New("option resolution failed")
	err := injectLogsAgent(s.T().Context(), injectLogsAgentParams{client: c})
	s.ErrorIs(err, c.infoErr)
}

func (s *logsEnvironmentSuite) TestRemoteInjectionDoesNotResolveOptions() {
	c := newLogsEnvironmentClient(provider.ExternalDriver)
	c.local = false
	c.infoErr = errors.New("remote options must not be requested")
	environment, err := logsAgentEnvironment(c)
	s.Require().NoError(err)
	s.Nil(environment)
	s.Zero(c.infoCalls)
}

type logsEnvironmentClient struct {
	client.WorkspaceClient
	info      *provider.AgentWorkspaceInfo
	infoErr   error
	local     bool
	infoCalls int
}

func (c *logsEnvironmentClient) AgentLocal() bool { return c.local }
func (*logsEnvironmentClient) AgentPath() string  { return "" }
func (*logsEnvironmentClient) AgentURL() string   { return "" }

func (c *logsEnvironmentClient) AgentInfo(
	provider.CLIOptions,
) (string, *provider.AgentWorkspaceInfo, error) {
	c.infoCalls++
	return "", c.info, c.infoErr
}

func newLogsEnvironmentClient(driver string) *logsEnvironmentClient {
	return &logsEnvironmentClient{
		local: true,
		info: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{Driver: driver, Local: config.BoolTrue},
			Options: map[string]config.OptionValue{
				"MSB_PATH":           {Value: "configured runtime"},
				"MSB_LIBKRUNFW_PATH": {Value: "configured firmware"},
			},
			Workspace: &provider.Workspace{
				ID: "logs-environment", Context: config.DefaultContext,
				Provider: provider.WorkspaceProviderConfig{
					Name:    "logs-external",
					Options: map[string]config.OptionValue{"MSB_HOME": {Value: "workspace home"}},
				},
			},
		},
	}
}
