package devcontainer

import (
	"encoding/json"
	"testing"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/compress"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

const (
	testTerminalSecretName    = "API_KEY"
	testTransportArchitecture = "arm64"
)

// Secret values must never ride in the compressed CLIOptions embedded in the
// setup command's arguments; the container pulls them over the tunnel instead.
func TestCompressWorkspaceConfig_StripsSecretValues(t *testing.T) {
	r := &runner{
		workspaceConfig: &provider.AgentWorkspaceInfo{
			Workspace: &provider.Workspace{},
			CLIOptions: provider.CLIOptions{
				SecretsEnv:             []string{"API_KEY=super-secret"},
				TerminalSecretEnvNames: []string{testTerminalSecretName},
				SecretsMount:           []string{"tls.key=key-material"},
				BuildSecrets:           []string{"NPM_TOKEN=npm-secret"},
				GitToken:               &provider.GitToken{Host: "github.com", Token: "ghp_secret"},
				Secrets:                []string{"API_KEY"},
				EnvVars:                []string{"LOG_LEVEL"},
				BuildSecretNames:       []string{"NPM_TOKEN"},
				GitTokenSecret:         "GH_TOKEN",
				GitTokenUsername:       "x-access-token",
			},
		},
	}

	compressed, err := r.compressWorkspaceConfig(&config.MergedDevContainerConfig{}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	decompressed, err := compress.Decompress(compressed)
	if err != nil {
		t.Fatal(err)
	}

	var info provider.ContainerWorkspaceInfo
	if err := json.Unmarshal([]byte(decompressed), &info); err != nil {
		t.Fatal(err)
	}

	o := info.CLIOptions
	stripped := map[string]bool{
		"SecretsEnv": o.SecretsEnv == nil,
		"TerminalSecretEnvNames": len(o.TerminalSecretEnvNames) == 1 &&
			o.TerminalSecretEnvNames[0] == testTerminalSecretName,
		"SecretsMount":     o.SecretsMount == nil,
		"BuildSecrets":     o.BuildSecrets == nil,
		"GitToken":         o.GitToken == nil,
		"Secrets":          o.Secrets == nil,
		"EnvVars":          o.EnvVars == nil,
		"BuildSecretNames": o.BuildSecretNames == nil,
		"GitTokenSecret":   o.GitTokenSecret == "",
		"GitTokenUsername": o.GitTokenUsername == "",
	}
	for field, ok := range stripped {
		if !ok {
			t.Errorf("%s must be stripped from the setup-command CLIOptions", field)
		}
	}
}

func TestCompressWorkspaceConfigKeepsImageConfigFileLocal(t *testing.T) {
	const path = "/operator/image-config.json"
	r := &runner{
		workspaceConfig: &provider.AgentWorkspaceInfo{
			Workspace: &provider.Workspace{},
			Agent: provider.ProviderAgentConfig{
				Kubernetes: provider.ProviderKubernetesDriverConfig{
					ImageConfigFile: path,
					Architecture:    testTransportArchitecture,
					ServiceAccount:  "cluster-account",
				},
			},
		},
	}
	compressed, err := r.compressWorkspaceConfig(&config.MergedDevContainerConfig{}, false, false)
	require.NoError(t, err)
	info, raw, err := agent.DecodeContainerWorkspaceInfo(compressed)
	require.NoError(t, err)
	require.Empty(t, info.Agent.Kubernetes.ImageConfigFile)
	require.NotContains(t, raw, path)
	require.Equal(t, path, r.workspaceConfig.Agent.Kubernetes.ImageConfigFile)
	require.Equal(t, testTransportArchitecture, info.Agent.Kubernetes.Architecture)
	require.Equal(t, "cluster-account", info.Agent.Kubernetes.ServiceAccount)
}
