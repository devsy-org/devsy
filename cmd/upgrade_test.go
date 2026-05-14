package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testFeatureGo     = "ghcr.io/devcontainers/features/go"
	testFeatureGoV121 = "1.21"
	testFeatureGoV123 = "1.23"
)

func TestNewUpgradeCmd_CreatesCommand(t *testing.T) {
	cmd := NewUpgradeCmd(nil)
	assert.Equal(t, "upgrade [feature...]", cmd.Use)
	assert.NotEmpty(t, cmd.Short)
}

func TestNewUpgradeCmd_HasDryRunFlag(t *testing.T) {
	cmd := NewUpgradeCmd(nil)
	flag := cmd.Flags().Lookup("dry-run")
	require.NotNil(t, flag)
	assert.Equal(t, "false", flag.DefValue)
}

func TestNewUpgradeCmd_HasWorkspaceFolderFlag(t *testing.T) {
	cmd := NewUpgradeCmd(nil)
	flag := cmd.Flags().Lookup("workspace-folder")
	require.NotNil(t, flag)
}

func TestNewUpgradeCmd_HasConfigFlag(t *testing.T) {
	cmd := NewUpgradeCmd(nil)
	flag := cmd.Flags().Lookup("config")
	require.NotNil(t, flag)
}

func TestNormalizeFeatureRef_WithTag(t *testing.T) {
	result := normalizeFeatureRef(testFeatureGo + ":" + testFeatureGoV121)
	assert.Equal(t, testFeatureGo, result)
}

func TestNormalizeFeatureRef_WithoutTag(t *testing.T) {
	result := normalizeFeatureRef(testFeatureGo)
	assert.Equal(t, testFeatureGo, result)
}

func TestNormalizeFeatureRef_BarePort(t *testing.T) {
	result := normalizeFeatureRef("localhost:5000")
	assert.Equal(t, "localhost:5000", result)
}

func TestMatchesTarget_ExactMatch(t *testing.T) {
	targets := map[string]bool{testFeatureGo: true}
	assert.True(t, matchesTarget(testFeatureGo+":"+testFeatureGoV121, targets))
}

func TestMatchesTarget_NoMatch(t *testing.T) {
	targets := map[string]bool{"ghcr.io/devcontainers/features/node": true}
	assert.False(t, matchesTarget(testFeatureGo+":"+testFeatureGoV121, targets))
}

func TestMatchesTarget_FullRefWithTag(t *testing.T) {
	targets := map[string]bool{testFeatureGo: true}
	assert.True(t, matchesTarget(testFeatureGo+":"+testFeatureGoV121, targets))
}

func TestMatchesTarget_RawFeatureID(t *testing.T) {
	targets := map[string]bool{testFeatureGo + ":" + testFeatureGoV121: true}
	assert.True(t, matchesTarget(testFeatureGo+":"+testFeatureGoV121, targets))
}

func TestUpgradeCmd_FindUpgradeable_FiltersTargets(t *testing.T) {
	cmd := &UpgradeCmd{}
	features := map[string]any{
		"./local-feature": map[string]any{},
	}

	results := cmd.findUpgradeable(features, nil)
	assert.Empty(t, results)
}

func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "devcontainer.json")

	err := os.WriteFile(configPath, []byte(content), 0o644) //nolint:gosec // G306
	require.NoError(t, err)

	return configPath
}

func TestUpgradeCmd_ApplyUpgrades_WritesFile(t *testing.T) {
	configPath := writeTestConfig(t, `{
  "features": {
    "ghcr.io/devcontainers/features/go:1.21": {},
    "ghcr.io/devcontainers/features/node:18": {}
  }
}`)

	cmd := &UpgradeCmd{}
	outdated := []outdatedEntry{
		{repo: testFeatureGo, current: testFeatureGoV121, latest: testFeatureGoV123},
	}

	err := cmd.applyUpgrades(configPath, outdated)
	require.NoError(t, err)

	result, err := os.ReadFile(configPath) //nolint:gosec // G304 -- test file
	require.NoError(t, err)

	assert.Contains(t, string(result), testFeatureGo+":"+testFeatureGoV123)
	assert.Contains(t, string(result), "ghcr.io/devcontainers/features/node:18")
	assert.NotContains(t, string(result), testFeatureGo+":"+testFeatureGoV121)
}

func TestUpgradeCmd_ApplyUpgrades_PreservesFormatting(t *testing.T) {
	configPath := writeTestConfig(t, `{
  // This is a comment
  "features": {
    "ghcr.io/devcontainers/features/go:1.21": {}
  }
}`)

	cmd := &UpgradeCmd{}
	outdated := []outdatedEntry{
		{repo: testFeatureGo, current: testFeatureGoV121, latest: testFeatureGoV123},
	}

	err := cmd.applyUpgrades(configPath, outdated)
	require.NoError(t, err)

	result, err := os.ReadFile(configPath) //nolint:gosec // G304 -- test file
	require.NoError(t, err)

	assert.Contains(t, string(result), "// This is a comment")
	assert.Contains(t, string(result), testFeatureGo+":"+testFeatureGoV123)
}

func TestUpgradeCmd_ApplyUpgrades_MultipleFeatures(t *testing.T) {
	configPath := writeTestConfig(t, `{
  "features": {
    "ghcr.io/devcontainers/features/go:1.21": {},
    "ghcr.io/devcontainers/features/node:18": {}
  }
}`)

	cmd := &UpgradeCmd{}
	outdated := []outdatedEntry{
		{repo: testFeatureGo, current: testFeatureGoV121, latest: testFeatureGoV123},
		{repo: "ghcr.io/devcontainers/features/node", current: "18", latest: "22"},
	}

	err := cmd.applyUpgrades(configPath, outdated)
	require.NoError(t, err)

	result, err := os.ReadFile(configPath) //nolint:gosec // G304 -- test file
	require.NoError(t, err)

	assert.Contains(t, string(result), testFeatureGo+":"+testFeatureGoV123)
	assert.Contains(t, string(result), "ghcr.io/devcontainers/features/node:22")
}
