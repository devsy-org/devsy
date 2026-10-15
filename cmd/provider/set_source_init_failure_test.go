package provider

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestSetSourceInitFailureKeepsCommittedReplacementAndWorkspace(t *testing.T) {
	initial, userOption := setupInitializedSourceProvider(t)

	associated := &provider2.Workspace{
		ID: "existing", UID: "existing-generation", Context: initial.DefaultContext,
		Provider: provider2.WorkspaceProviderConfig{Name: testProviderFoo},
	}
	require.NoError(t, provider2.SaveWorkspaceConfig(associated))
	workspaceDir, err := provider2.GetWorkspaceDir(initial.DefaultContext, associated.ID)
	require.NoError(t, err)
	recordPath := filepath.Join(workspaceDir, provider2.WorkspaceConfigFile)
	// #nosec G304 -- recordPath is the isolated fixture workspace record.
	recordBefore, err := os.ReadFile(recordPath)
	require.NoError(t, err)
	markerPath := filepath.Join(workspaceDir, "retained-content")
	require.NoError(t, os.WriteFile(markerPath, []byte("existing-workspace-content"), 0o600))

	missingInit := filepath.Join(t.TempDir(), "missing-init-executable")
	replacement, err := provider2.LoadProviderConfig(initial.DefaultContext, testProviderFoo)
	require.NoError(t, err)
	replacement.Version = "2.0.0"
	replacement.Exec.Init = []string{missingInit}
	replacementSource := filepath.Join(t.TempDir(), "replacement.yaml")
	replacement.Source = provider2.ProviderSource{File: replacementSource, Raw: replacementSource}
	replacementBytes, err := yaml.Marshal(replacement)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(replacementSource, replacementBytes, 0o600))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := &SetSourceCmd{GlobalFlags: &flags.GlobalFlags{}, Use: true}
	err = cmd.Run(ctx, initial, []string{testProviderFoo, replacementSource})
	require.ErrorContains(t, err, "init:")
	require.ErrorContains(t, err, "missing-init-executable")

	assertFailedSourceReplacement(
		t,
		initial.DefaultContext,
		replacement,
		userOption,
	)
	// #nosec G304 -- recordPath is the same isolated fixture workspace record.
	recordAfter, err := os.ReadFile(recordPath)
	require.NoError(t, err)
	require.Equal(t, recordBefore, recordAfter)
	// #nosec G304 -- markerPath is created in this fixture workspace directory.
	markerAfter, err := os.ReadFile(markerPath)
	require.NoError(t, err)
	require.Equal(t, "existing-workspace-content", string(markerAfter))
}

func setupInitializedSourceProvider(t *testing.T) (*config.Config, config.OptionValue) {
	t.Helper()
	t.Setenv("DEVSY_HOME", t.TempDir())
	initial := setupProviderOperationTest(t)
	oldSource := filepath.Join(t.TempDir(), "old.yaml")
	old := &provider2.ProviderConfig{
		Name: testProviderFoo, Version: "1.0.0",
		Options: map[string]*types.Option{"REGION": {Default: "default-region"}},
		Exec:    provider2.ProviderCommands{Command: []string{"unused-command"}},
	}
	oldBytes, err := yaml.Marshal(old)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(oldSource, oldBytes, 0o600))
	_, err = workspace.AddProvider(context.Background(), initial, testProviderFoo, oldSource)
	require.NoError(t, err)
	userOption := config.OptionValue{Value: "user-region", UserProvided: true}
	require.NoError(
		t,
		config.UpdateConfig(initial.DefaultContext, "", func(c *config.Config) error {
			c.Current().DefaultProvider = testProviderFoo
			c.Current().Providers[testProviderFoo].Initialized = true
			c.Current().Providers[testProviderFoo].Options = map[string]config.OptionValue{
				"REGION": userOption,
			}
			return nil
		}),
	)

	return initial, userOption
}

func assertFailedSourceReplacement(
	t *testing.T,
	contextName string,
	expected *provider2.ProviderConfig,
	userOption config.OptionValue,
) {
	t.Helper()
	installed, err := provider2.LoadProviderConfig(contextName, testProviderFoo)
	require.NoError(t, err)
	require.Equal(t, expected.Version, installed.Version)
	require.Equal(t, expected.Source.Raw, installed.Source.Raw)
	require.Equal(t, expected.Exec.Init, installed.Exec.Init)
	stored, err := config.LoadConfig(contextName, "")
	require.NoError(t, err)
	state := stored.Current().Providers[testProviderFoo]
	require.NotNil(t, state)
	require.False(t, state.Initialized)
	require.True(t, state.InitAttempted)
	require.Contains(t, state.InitError, "missing-init-executable")
	require.Equal(t, userOption.Value, state.Options["REGION"].Value)
	require.True(t, state.Options["REGION"].UserProvided)
	require.Equal(t, testProviderFoo, stored.Current().DefaultProvider)
}
