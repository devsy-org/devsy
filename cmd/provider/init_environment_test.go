package provider

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/stretchr/testify/assert"
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

func TestProviderInitFailureRetainsRedactedStderrAndExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	t.Setenv(config.EnvHome, t.TempDir())
	const optionSecret = "opaque-provider-secret-78153"
	const dynamicSecret = "dynamic-provider-secret-78155"
	const hostSecret = "host-provider-secret-78154"
	t.Setenv("EXTERNAL_TOKEN", hostSecret)
	cfg := setupProviderOperationTest(t)
	cfg.Current().Providers[testProviderFoo] = &config.ProviderConfig{
		Options: map[string]config.OptionValue{
			"OPAQUE":         {Value: optionSecret},
			"DYNAMIC_OPAQUE": {Value: dynamicSecret},
		},
		DynamicOptions: config.OptionDefinitions{"DYNAMIC_OPAQUE": {Password: true}},
	}
	require.NoError(t, config.SaveConfig(cfg))
	definition := &provider2.ProviderConfig{
		Name: testProviderFoo,
		Options: map[string]*types.Option{
			"OPAQUE": {Password: true},
		},
		Exec: provider2.ProviderCommands{
			Init: []string{
				"/bin/sh", "-c",
				`printf 'stdout-only-canary'; ` +
					`printf 'stderr-canary %s %s %s' "$OPAQUE" "$DYNAMIC_OPAQUE" "$EXTERNAL_TOKEN" >&2; exit 23`,
			},
		},
	}
	err := runProviderInit(
		context.Background(),
		ProviderOptionsConfig{Provider: definition},
		cfg,
		nil,
	)
	require.Error(t, err)
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr))
	require.Equal(t, 23, exitErr.ExitCode())
	diagnostic := clierr.Classify(err).Context["stderr"]
	assert.Contains(t, diagnostic, "stderr-canary")
	assert.NotContains(t, diagnostic, "stdout-only-canary")
	assert.NotContains(t, diagnostic, optionSecret)
	assert.NotContains(t, diagnostic, dynamicSecret)
	assert.NotContains(t, diagnostic, hostSecret)
	assert.Contains(t, diagnostic, "***")

	definition.Exec.Init = []string{"/bin/sh", "-c", "exit 19"}
	err = runProviderInit(
		context.Background(),
		ProviderOptionsConfig{Provider: definition},
		cfg,
		nil,
	)
	require.Error(t, err)
	assert.Empty(t, clierr.Classify(err).Context)
	require.True(t, errors.As(err, &exitErr))
	assert.Equal(t, 19, exitErr.ExitCode())
}
