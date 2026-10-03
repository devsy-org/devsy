package options

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/require"
)

func TestResolveExternalAgentConfig(t *testing.T) {
	for _, backend := range []string{"", "docker", "none"} {
		t.Run(backend, func(t *testing.T) {
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
			require.Equal(t, expected, resolved.External.ImageBackend)
			require.Equal(t, "RUNTIME", resolved.External.Binary)
			require.Equal(t, source.Agent.External.Args, resolved.External.Args)
			require.Equal(t, backend, source.Agent.External.ImageBackend)
		})
	}
}
