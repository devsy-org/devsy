package devsyconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

const (
	testContext      = "default"
	testProviderName = "docker"
)

func useTempDevsyHome(t *testing.T) {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	config.SetPathManager(config.NewPathManager())
	t.Cleanup(config.ResetPathManager)
}

func testWorkspace(contextName, providerName string) *provider.Workspace {
	ws := &provider.Workspace{Context: contextName}
	ws.Provider.Name = providerName
	return ws
}

func providerConfigPath(t *testing.T, contextName, providerName string) string {
	t.Helper()
	dir, err := provider.GetProviderDir(contextName, providerName)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(dir, 0o750))
	return filepath.Join(dir, platform.DevsyPlatformConfigFileName)
}

func writePlatformConfig(t *testing.T, contextName, providerName, content string) {
	t.Helper()
	path := providerConfigPath(t, contextName, providerName)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestReadReturnsEmptyConfigWhenProviderConfigMissing(t *testing.T) {
	useTempDevsyHome(t)

	resp, err := Read(testWorkspace(testContext, testProviderName))

	require.NoError(t, err)
	require.Equal(t, &client.Config{}, resp.DevsyConfig)
}

func TestReadDecodesPersistedPlatformConfig(t *testing.T) {
	useTempDevsyHome(t)
	writePlatformConfig(t, testContext, testProviderName,
		`{"host":"https://platform.example","accesskey":"key-123","lastInstallContext":"prod"}`)

	resp, err := Read(testWorkspace(testContext, testProviderName))

	require.NoError(t, err)
	require.Equal(t, "https://platform.example", resp.DevsyConfig.Host)
	require.Equal(t, "key-123", resp.DevsyConfig.AccessKey)
	require.Equal(t, "prod", resp.DevsyConfig.LastInstallContext)
}

func TestReadScopesConfigToContextAndProvider(t *testing.T) {
	useTempDevsyHome(t)
	writePlatformConfig(t, testContext, testProviderName,
		`{"host":"https://platform.example"}`)

	otherProvider, err := Read(testWorkspace(testContext, "kubernetes"))
	require.NoError(t, err)
	require.Empty(t, otherProvider.DevsyConfig.Host)

	otherContext, err := Read(testWorkspace("staging", testProviderName))
	require.NoError(t, err)
	require.Empty(t, otherContext.DevsyConfig.Host)
}

func TestReadRejectsMalformedPlatformConfig(t *testing.T) {
	useTempDevsyHome(t)
	writePlatformConfig(t, testContext, testProviderName, `{"host":`)

	resp, err := Read(testWorkspace(testContext, testProviderName))

	require.Error(t, err)
	require.Nil(t, resp)
}

func TestReadFailsWhenConfigPathIsDirectory(t *testing.T) {
	useTempDevsyHome(t)
	path := providerConfigPath(t, testContext, testProviderName)
	require.NoError(t, os.MkdirAll(path, 0o750))

	resp, err := Read(testWorkspace(testContext, testProviderName))

	require.Error(t, err)
	require.Nil(t, resp)
}
