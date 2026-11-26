package cmdinternal

import (
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/require"
)

func TestLoadCurrentProviderUsesLatestGitHubSourceWithoutChangingPin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(config.EnvHome, filepath.Join(home, ".devsy"))
	config.ResetPathManager()
	t.Cleanup(config.ResetPathManager)

	const name = "microsandbox"
	cfg := &config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{
			config.DefaultContext: {
				Providers: map[string]*config.ProviderConfig{name: {Initialized: true}},
			},
		},
	}
	require.NoError(t, config.SaveConfig(cfg))
	const repo = "github.com/devsy-org/devsy-provider-microsandbox"
	const pinned = repo + "@v0.1.8"
	stored := &provider.ProviderConfig{
		Name: name, Version: "v0.1.8",
		Exec: provider.ProviderCommands{Command: []string{"true"}},
		Source: provider.ProviderSource{
			Github: "devsy-org/devsy-provider-microsandbox", Raw: pinned,
		},
	}
	require.NoError(t, provider.SaveProviderConfig(config.DefaultContext, stored))
	reloadSource, err := workspace.ResolveProviderSource(cfg, name)
	require.NoError(t, err)
	require.Equal(t, pinned, reloadSource, "a reload must retain the installed pin")

	source, version, err := loadCurrentProvider(cfg, name)
	require.NoError(t, err)
	require.Equal(t, repo, source, "the update probe must fetch latest, not the installed pin")
	require.Equal(t, stored.Version, version)
	loaded, err := provider.LoadProviderConfig(config.DefaultContext, name)
	require.NoError(t, err)
	require.Equal(t, stored.Source, loaded.Source, "checking for updates must not mutate the pin")
}
