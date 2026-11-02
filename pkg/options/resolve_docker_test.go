package options

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
)

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
					"INSTALL_DOCKER":   {Value: "false"},
					"CONFIGURE_DAEMON": {Value: "false"},
				}},
			}},
		},
	}
	resolved := ResolveAgentConfig(devConfig, manifest, nil, nil)
	if resolved.Docker.ConfigureDaemon != types.StrBool("false") ||
		resolved.Docker.Install != types.StrBool("false") {
		t.Fatalf("resolved Docker management = %+v, want disabled", resolved.Docker)
	}
	if manifest.Agent.Docker.ConfigureDaemon != "${CONFIGURE_DAEMON}" {
		t.Fatalf("original provider policy was mutated: %q", manifest.Agent.Docker.ConfigureDaemon)
	}
	data, err := json.Marshal(&provider.AgentWorkspaceInfo{Agent: resolved})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"configureDaemon":"false"`) ||
		!strings.Contains(string(data), `"install":"false"`) {
		t.Fatalf("agent workspace lost disabled Docker management policy: %s", data)
	}
	var decoded provider.AgentWorkspaceInfo
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Agent.Docker.ConfigureDaemon != "false" || decoded.Agent.Docker.Install != "false" {
		t.Fatalf("decoded agent workspace lost policy: %+v", decoded.Agent.Docker)
	}
}
