package up

import (
	"bytes"
	"os"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/hash"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
)

type externalEnvironmentSuite struct{ suite.Suite }

func TestExternalEnvironment(t *testing.T) { suite.Run(t, new(externalEnvironmentSuite)) }

func (s *externalEnvironmentSuite) TestLocalInjection() {
	s.T().Setenv(config.EnvHome, s.T().TempDir())
	s.T().Setenv("MSB_PATH", "ambient-runtime")
	for _, disabled := range []bool{false, true} {
		name := "daemon"
		if disabled {
			name = "disabled"
		}
		s.Run(name, func() {
			client := s.newClient()
			compressed, info, err := client.AgentInfo(provider.CLIOptions{DisableDaemon: disabled})
			s.Require().NoError(err)
			payload, _, err := agent.DecodeWorkspaceInfo(compressed)
			s.Require().NoError(err)
			s.Equal(!disabled, len(payload.Options) > 0)
			s.Equal(!disabled, len(payload.Workspace.Provider.Options) > 0)
			var stdout bytes.Buffer
			err = newAgentInjectFunc(
				client,
				info,
			)(
				s.T().Context(),
				`printf '%s|%s|%s' "$MSB_PATH" "$MSB_LIBKRUNFW_PATH" "$MSB_HOME"`,
				nil,
				&stdout,
				nil,
			)
			s.Require().NoError(err)
			s.Equal("configured runtime|configured firmware|workspace home", stdout.String())
			s.Equal("ambient-runtime", os.Getenv("MSB_PATH"))
		})
	}
}

const environmentProvider = "external-test"

func (s *externalEnvironmentSuite) newClient() client.WorkspaceClient {
	cfg := &config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{
			config.DefaultContext: {
				Providers: map[string]*config.ProviderConfig{
					environmentProvider: {
						Options: map[string]config.OptionValue{
							"MSB_PATH":           {Value: "configured runtime"},
							"MSB_LIBKRUNFW_PATH": {Value: "configured firmware"},
						},
					},
				},
			},
		},
	}
	workspace := &provider.Workspace{
		ID:      "environment",
		Context: config.DefaultContext,
		Provider: provider.WorkspaceProviderConfig{
			Name:    environmentProvider,
			Options: map[string]config.OptionValue{"MSB_HOME": {Value: "workspace home"}},
		},
	}
	providerConfig := &provider.ProviderConfig{Name: environmentProvider}
	providerConfig.Agent.Driver = provider.ExternalDriver
	providerConfig.Agent.Local = config.BoolTrue
	providerConfig.Agent.External.Binary = "RUNTIME"
	providerConfig.Agent.Binaries = map[string][]*provider.ProviderBinary{
		"RUNTIME": {
			{
				OS:       runtime.GOOS,
				Arch:     runtime.GOARCH,
				Path:     "https://example.invalid/runtime",
				Checksum: hash.String("runtime"),
			},
		},
	}
	client, err := clientimplementation.NewWorkspaceClient(cfg, providerConfig, workspace, nil)
	s.Require().NoError(err)
	return client
}
