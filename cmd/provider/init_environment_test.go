package provider

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestInitProviderReceivesBaseEnvironment(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	cfg := setupProviderOperationTest(t)
	captured := filepath.Join(t.TempDir(), "captured")
	definition := &provider2.ProviderConfig{
		Name:    testProviderFoo,
		Version: "3.1.4",
		Exec: provider2.ProviderCommands{
			Command: []string{"unused-command"},
			Init: []string{
				`printf '%s\n%s' "$DEVSY" "$PROVIDER_ID" > '` + filepath.ToSlash(captured) + `'`,
			},
		},
	}
	raw, err := yaml.Marshal(definition)
	require.NoError(t, err)
	source := filepath.Join(t.TempDir(), "provider.yaml")
	require.NoError(t, os.WriteFile(source, raw, 0o600))
	installed, err := workspace.AddProvider(context.Background(), cfg, testProviderFoo, source)
	require.NoError(t, err)

	require.NoError(t, initProvider(
		context.Background(), cfg, installed, initIO{stdout: io.Discard, stderr: io.Discard},
	))

	executable, err := os.Executable()
	require.NoError(t, err)
	// #nosec G304 -- captured is created by the init command in this isolated fixture.
	got, err := os.ReadFile(captured)
	require.NoError(t, err)
	require.Equal(t, filepath.ToSlash(executable)+"\n"+testProviderFoo, string(got))
}
