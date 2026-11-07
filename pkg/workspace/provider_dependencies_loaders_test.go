package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/internal/dependencytest"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/dependencies"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/providers"
	"github.com/stretchr/testify/require"
)

func TestProviderDependencyReadOnlyLoaders(t *testing.T) {
	root := t.TempDir()
	t.Setenv(config.EnvHome, root)
	t.Setenv(config.EnvConfig, "")
	tool := writeProviderDependencyLoaderFixtures(t, root)
	pm := config.NewReadOnlyPathManager()
	dependencytest.Preserve(t, root)
	loadedConfig, err := config.LoadConfigWithPathManager(pm, "", "")
	require.NoError(t, err)
	loadedProvider, err := provider.LoadProviderConfigWithPathManager(
		pm,
		loadedConfig.DefaultContext,
		loadedConfig.Current().DefaultProvider,
	)
	require.NoError(t, err)
	request := ProviderDependencySurveyRequest{
		Provider: *loadedProvider, Options: loadedConfig.ProviderOptions(loadedProvider.Name),
		Environment: map[string]string{"PATH": ""}, WorkingDirectory: root,
		Target: dependencies.SurveyTarget{
			StoreID:      "fixture-store",
			ContextName:  loadedConfig.DefaultContext,
			ProviderName: loadedProvider.Name,
		},
		Host: dependencies.SurveyHost{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
	}
	require.Equal(t, tool, request.Options[dependencyAdapterOption].Value)
	before, err := json.Marshal(request)
	require.NoError(t, err)
	result, err := SurveyProviderDependencies(context.Background(), request)
	require.NoError(t, err)
	require.True(t, result.ContractVerified)
	require.Equal(t, DefaultDockerCommand, result.BindingID)
	require.False(t, result.Executed)
	require.Len(t, result.Requirements, 1)
	require.Equal(t, "available", result.Requirements[0].Availability)
	require.Len(t, result.Requirements[0].Candidates, 1)
	require.Equal(t, dependencies.LookupExplicit, result.Requirements[0].Candidates[0].Origin)
	require.NoFileExists(t, filepath.Join(root, "executed"))
	after, err := json.Marshal(request)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
}

func writeProviderDependencyLoaderFixtures(t *testing.T, root string) string {
	t.Helper()
	pm := config.NewReadOnlyPathManager()
	fixtures := &providerDependencySurveySuite{root: root, bin: root}
	fixtures.SetT(t)
	tool := fixtures.executable("loader-docker")
	manifest, err := provider.ParseProvider(
		strings.NewReader(providers.GetBuiltInProviders()[DefaultDockerCommand]),
	)
	require.NoError(t, err)
	manifest.Name = "custom-docker"
	manifest.Source = provider.ProviderSource{Internal: true, Raw: DefaultDockerCommand}
	manifestBytes, err := json.Marshal(manifest)
	require.NoError(t, err)
	providerDir, err := pm.ProviderDir(config.DefaultContext, manifest.Name)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(providerDir, 0o700))
	require.NoError(
		t,
		os.WriteFile(filepath.Join(providerDir, provider.ProviderConfigFile), manifestBytes, 0o600),
	)
	devsyConfig := config.Config{
		DefaultContext: config.DefaultContext,
		Contexts: map[string]*config.ContextConfig{config.DefaultContext: {
			DefaultProvider: manifest.Name,
			Providers: map[string]*config.ProviderConfig{manifest.Name: {
				Options: map[string]config.OptionValue{
					dependencyAdapterOption: {Value: tool, UserProvided: true},
				},
			}},
		}},
	}
	configBytes, err := json.Marshal(devsyConfig)
	require.NoError(t, err)
	configPath, err := pm.ConfigFilePath()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(configPath, configBytes, 0o600))
	return tool
}
