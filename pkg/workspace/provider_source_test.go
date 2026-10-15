package workspace

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestResolveProviderSourcePreservesPersistedGitHubPin(t *testing.T) {
	setupTestPathManager(t)
	t.Setenv(config.EnvHome, t.TempDir())
	config.ResetPathManager()

	const providerName = "pinned-microsandbox"
	devsyConfig := &config.Config{
		DefaultContext: testDefaultContext,
		Contexts: map[string]*config.ContextConfig{
			testDefaultContext: {
				Providers: map[string]*config.ProviderConfig{
					providerName: {},
				},
			},
		},
	}
	require.NoError(t, config.SaveConfig(devsyConfig))
	require.NoError(t, provider.SaveProviderConfig(testDefaultContext, &provider.ProviderConfig{
		Name:    providerName,
		Version: "v0.1.8",
		Exec:    provider.ProviderCommands{Command: []string{"true"}},
		Source: provider.ProviderSource{
			Github: "devsy-org/devsy-provider-microsandbox",
			Raw:    "devsy-provider-microsandbox@v0.1.8",
		},
	}))

	source, err := ResolveProviderSource(devsyConfig, providerName)
	require.NoError(t, err)
	require.Equal(
		t,
		"github.com/devsy-org/devsy-provider-microsandbox@v0.1.8",
		source,
	)
}
