package options

import (
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/require"
)

const disabledDockerPolicy = "false"

func TestResolveDockerDaemonPolicyIntoWorkspaceInfo(t *testing.T) {
	manifest := &provider.ProviderConfig{
		Name: "docker-policy",
		Agent: provider.ProviderAgentConfig{
			Driver: provider.DockerDriver,
			Docker: provider.ProviderDockerDriverConfig{
				Install:         "${INSTALL_DOCKER}",
				ConfigureDaemon: "${CONFIGURE_DAEMON}",
			},
		},
	}
	devConfig := &config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{
			config.DefaultContext: {Providers: map[string]*config.ProviderConfig{
				manifest.Name: {Options: map[string]config.OptionValue{
					"INSTALL_DOCKER":   {Value: disabledDockerPolicy},
					"CONFIGURE_DAEMON": {Value: disabledDockerPolicy},
				}},
			}},
		},
	}
	resolved := ResolveAgentConfig(devConfig, manifest, nil, nil)
	require.Equal(t, types.StrBool(disabledDockerPolicy), resolved.Docker.ConfigureDaemon)
	require.Equal(t, types.StrBool(disabledDockerPolicy), resolved.Docker.Install)
	require.Equal(t, "${CONFIGURE_DAEMON}", string(manifest.Agent.Docker.ConfigureDaemon))

	data, err := json.Marshal(&provider.AgentWorkspaceInfo{Agent: resolved})
	require.NoError(t, err)
	require.Contains(t, string(data), `"configureDaemon":"false"`)
	require.Contains(t, string(data), `"install":"false"`)

	var decoded provider.AgentWorkspaceInfo
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, types.StrBool(disabledDockerPolicy), decoded.Agent.Docker.ConfigureDaemon)
	require.Equal(t, types.StrBool(disabledDockerPolicy), decoded.Agent.Docker.Install)
}
