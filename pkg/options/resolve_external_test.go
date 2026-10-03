package options

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/suite"
)

type externalAgentConfigSuite struct{ suite.Suite }

func TestResolveExternalAgentConfig(t *testing.T) { suite.Run(t, new(externalAgentConfigSuite)) }

func (s *externalAgentConfigSuite) TestResolution() {
	for _, backend := range []string{"", "docker", "none"} {
		s.Run(backend, func() {
			source := &provider.ProviderConfig{
				Name: "external-fixture",
				Agent: provider.ProviderAgentConfig{
					Driver: "${CHOICE}",
					External: provider.ProviderExternalDriverConfig{
						Binary:       "RUNTIME",
						Args:         types.StrArray{"serve", "${STATIC}", ""},
						ImageBackend: backend,
					},
				},
			}
			devConfig := &config.Config{
				DefaultContext: config.DefaultContext,
				Contexts: map[string]*config.ContextConfig{
					config.DefaultContext: {Providers: map[string]*config.ProviderConfig{
						source.Name: {
							Options: map[string]config.OptionValue{
								"CHOICE":  {Value: provider.ExternalDriver},
								"STATIC":  {Value: "must-not-expand"},
								"RUNTIME": {Value: "must-not-become-a-path"},
							},
						},
					}},
				},
			}
			resolved := ResolveAgentConfig(devConfig, source, nil, nil)
			expected := backend
			if expected == "" {
				expected = provider.DockerDriver
			}
			s.Require().Equal(expected, resolved.External.ImageBackend)
			s.Require().Equal("RUNTIME", resolved.External.Binary)
			s.Require().Equal(source.Agent.External.Args, resolved.External.Args)
			s.Require().Equal(backend, source.Agent.External.ImageBackend)
		})
	}
}
