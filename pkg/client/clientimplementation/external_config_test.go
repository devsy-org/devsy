package clientimplementation

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/hash"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestResolvedExternalConfigurationHandoff(t *testing.T) {
	for _, driver := range []string{provider.DockerDriver, provider.ExternalDriver} {
		t.Run(driver, func(t *testing.T) {
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
				require.NoError(t, err)
				require.NotEmpty(t, compressed)
				require.Equal(t, driver, info.Agent.Driver)
				return
			}
			require.ErrorContains(t, err, "agent.external.binary")
			require.Empty(t, compressed)
			require.Nil(t, info)
			client.providerConfig.Agent.External.Binary = "RUNTIME"
			client.providerConfig.Agent.Binaries = map[string][]*provider.ProviderBinary{
				"RUNTIME": {{OS: "linux", Arch: "amd64", Path: "https://example.invalid/runtime"}},
			}
			_, _, err = client.AgentInfo(provider.CLIOptions{})
			require.ErrorContains(t, err, "SHA-256")
			client.providerConfig.Agent.Binaries["RUNTIME"][0].Checksum = hash.String("runtime")
			compressed, info, err = client.AgentInfo(provider.CLIOptions{})
			require.NoError(t, err)
			require.NotEmpty(t, compressed)
			require.Equal(t, driver, info.Agent.Driver)
			require.Equal(t, provider.DockerDriver, info.Agent.External.ImageBackend)
		})
	}
}
