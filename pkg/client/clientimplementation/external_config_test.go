package clientimplementation

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/hash"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/suite"
)

type externalHandoffSuite struct{ suite.Suite }

func TestResolvedExternalConfigurationHandoff(
	t *testing.T,
) {
	suite.Run(t, new(externalHandoffSuite))
}

func (s *externalHandoffSuite) TestResolution() {
	for _, driver := range []string{provider.DockerDriver, provider.ExternalDriver} {
		s.Run(driver, func() {
			client := &workspaceClient{
				providerConfig: &provider.ProviderConfig{
					Name:  "external-handoff",
					Agent: provider.ProviderAgentConfig{Driver: "${DRIVER}"},
				},
				devsyConfig: &config.Config{
					DefaultContext: config.DefaultContext,
					Contexts: map[string]*config.ContextConfig{
						config.DefaultContext: {Providers: map[string]*config.ProviderConfig{
							"external-handoff": {
								Options: map[string]config.OptionValue{"DRIVER": {Value: driver}},
							},
						}},
					},
				},
			}
			compressed, info, err := client.AgentInfo(provider.CLIOptions{})
			if driver == provider.DockerDriver {
				s.Require().NoError(err)
				s.Require().NotEmpty(compressed)
				s.Require().Equal(driver, info.Agent.Driver)
				return
			}
			s.Require().ErrorContains(err, "agent.external.binary")
			s.Require().Empty(compressed)
			s.Require().Nil(info)
			client.providerConfig.Agent.External.Binary = "RUNTIME"
			client.providerConfig.Agent.Binaries = map[string][]*provider.ProviderBinary{
				"RUNTIME": {{OS: "linux", Arch: "amd64", Path: "https://example.invalid/runtime"}},
			}
			_, _, err = client.AgentInfo(provider.CLIOptions{})
			s.Require().ErrorContains(err, "SHA-256")
			client.providerConfig.Agent.Binaries["RUNTIME"][0].Checksum = hash.String("runtime")
			compressed, info, err = client.AgentInfo(provider.CLIOptions{})
			s.Require().NoError(err)
			s.Require().NotEmpty(compressed)
			s.Require().Equal(driver, info.Agent.Driver)
			s.Require().Equal(provider.DockerDriver, info.Agent.External.ImageBackend)
		})
	}
}
