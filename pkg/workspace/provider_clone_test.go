package workspace

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestCloneProviderCreatesIndependentState(t *testing.T) {
	setupTestPathManager(t)
	initial := &config.Config{
		DefaultContext: "default",
		Contexts: map[string]*config.ContextConfig{
			"default": {Providers: map[string]*config.ProviderConfig{
				"source": {Initialized: true},
			}},
		},
	}
	require.NoError(t, config.SaveConfig(initial))
	require.NoError(t, provider.SaveProviderConfig("default", &provider.ProviderConfig{
		Name: "source",
		Exec: provider.ProviderCommands{Command: []string{"true"}},
	}))

	clone, err := CloneProvider(context.Background(), initial, "copy", "source")
	require.NoError(t, err)
	require.Equal(t, "copy", clone.Config.Name)
	require.True(t, clone.State.Initialized)

	stored, err := config.LoadConfig("default", "")
	require.NoError(t, err)
	require.NotNil(t, stored.Current().Providers["copy"])
	require.False(t, stored.Current().Providers["copy"].Initialized)
	require.True(t, stored.Current().Providers["source"].Initialized)
}
