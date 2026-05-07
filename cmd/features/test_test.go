package features

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTestCmd_FlagDefaults(t *testing.T) {
	cmd := NewTestCmd(nil)

	projectFolderFlag := cmd.Flags().Lookup("project-folder")
	require.NotNil(t, projectFolderFlag)
	assert.Equal(t, "", projectFolderFlag.DefValue)

	featuresFlag := cmd.Flags().Lookup("features")
	require.NotNil(t, featuresFlag)
	assert.Equal(t, "", featuresFlag.DefValue)

	baseImageFlag := cmd.Flags().Lookup("base-image")
	require.NotNil(t, baseImageFlag)
	assert.Equal(t, defaultBaseImage, baseImageFlag.DefValue)

	remoteUserFlag := cmd.Flags().Lookup("remote-user")
	require.NotNil(t, remoteUserFlag)
	assert.Equal(t, "root", remoteUserFlag.DefValue)

	skipScenariosFlag := cmd.Flags().Lookup("skip-scenarios")
	require.NotNil(t, skipScenariosFlag)
	assert.Equal(t, "false", skipScenariosFlag.DefValue)

	quietFlag := cmd.Flags().Lookup("quiet")
	require.NotNil(t, quietFlag)
	assert.Equal(t, "false", quietFlag.DefValue)

	preserveFlag := cmd.Flags().Lookup("preserve-test-containers")
	require.NotNil(t, preserveFlag)
	assert.Equal(t, "false", preserveFlag.DefValue)
}

func TestTestCmd_AllFlagsRegistered(t *testing.T) {
	cmd := NewTestCmd(nil)
	expected := []string{
		"project-folder",
		"features",
		"base-image",
		"remote-user",
		"skip-scenarios",
		"quiet",
		"preserve-test-containers",
	}
	for _, name := range expected {
		assert.NotNil(t, cmd.Flags().Lookup(name), "flag %q should be registered", name)
	}
}

func TestTestCmd_DiscoverFeatures(t *testing.T) {
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src")

	featureADir := filepath.Join(srcDir, "feature-a")
	require.NoError(t, os.MkdirAll(featureADir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(featureADir, "devcontainer-feature.json"),
		[]byte(`{"id":"feature-a","version":"1.0.0","name":"Feature A"}`),
		0o600,
	))

	featureBDir := filepath.Join(srcDir, "feature-b")
	require.NoError(t, os.MkdirAll(featureBDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(featureBDir, "devcontainer-feature.json"),
		[]byte(`{"id":"feature-b","version":"2.0.0","name":"Feature B"}`),
		0o600,
	))

	require.NoError(t, os.WriteFile(
		filepath.Join(srcDir, "not-a-dir.txt"),
		[]byte("ignored"),
		0o600,
	))

	cmd := &TestCmd{}
	features, err := cmd.discoverFeatures(srcDir)
	require.NoError(t, err)
	assert.Len(t, features, 2)

	ids := make(map[string]bool)
	for _, f := range features {
		ids[f.id] = true
	}
	assert.True(t, ids["feature-a"])
	assert.True(t, ids["feature-b"])
}

func TestTestCmd_DiscoverFeatures_EmptyDir(t *testing.T) {
	projectDir := t.TempDir()
	srcDir := filepath.Join(projectDir, "src")
	require.NoError(t, os.MkdirAll(srcDir, 0o700))

	cmd := &TestCmd{}
	_, err := cmd.discoverFeatures(srcDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no features found")
}

func TestTestCmd_DiscoverFeatures_NonexistentDir(t *testing.T) {
	cmd := &TestCmd{}
	_, err := cmd.discoverFeatures("/nonexistent/path/src")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read src/ directory")
}

func TestTestCmd_FilterFeatures(t *testing.T) {
	features := []featureEntry{
		{id: "go", config: nil},
		{id: "node", config: nil},
		{id: "python", config: nil},
	}

	t.Run("empty filter returns all", func(t *testing.T) {
		cmd := &TestCmd{Features: ""}
		result := cmd.filterFeatures(features)
		assert.Len(t, result, 3)
	})

	t.Run("single filter", func(t *testing.T) {
		cmd := &TestCmd{Features: "go"}
		result := cmd.filterFeatures(features)
		assert.Len(t, result, 1)
		assert.Equal(t, "go", result[0].id)
	})

	t.Run("multi filter", func(t *testing.T) {
		cmd := &TestCmd{Features: "go,python"}
		result := cmd.filterFeatures(features)
		assert.Len(t, result, 2)
	})

	t.Run("filter with spaces", func(t *testing.T) {
		cmd := &TestCmd{Features: " go , node "}
		result := cmd.filterFeatures(features)
		assert.Len(t, result, 2)
	})

	t.Run("no match", func(t *testing.T) {
		cmd := &TestCmd{Features: "rust"}
		result := cmd.filterFeatures(features)
		assert.Empty(t, result)
	})
}

func TestTestCmd_GenerateDockerfile(t *testing.T) {
	t.Run("basic dockerfile", func(t *testing.T) {
		df := GenerateDockerfileForTest("my-feature", "ubuntu:22.04", "root", nil)
		assert.Contains(t, df, "FROM ubuntu:22.04")
		assert.Contains(t, df, "COPY src/my-feature /tmp/build-features/my-feature")
		assert.Contains(t, df, "RUN chmod +x /tmp/build-features/my-feature/install.sh")
		assert.NotContains(t, df, "USER")
	})

	t.Run("with remote user", func(t *testing.T) {
		df := GenerateDockerfileForTest("my-feature", "ubuntu:22.04", "vscode", nil)
		assert.Contains(t, df, "USER vscode")
	})

	t.Run("with options", func(t *testing.T) {
		opts := map[string]string{"version": "1.21"}
		df := GenerateDockerfileForTest("go", "ubuntu:22.04", "root", opts)
		assert.Contains(t, df, "ENV GO_VERSION=1.21")
	})

	t.Run("default base image", func(t *testing.T) {
		df := GenerateDockerfileForTest("feat", defaultBaseImage, "root", nil)
		assert.Contains(t, df, "FROM "+defaultBaseImage)
	})
}

func TestTestCmd_LoadScenarioOptions(t *testing.T) {
	t.Run("valid scenario.json", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, "scenario.json"),
			[]byte(`{"options":{"version":"3.11","installTools":"true"}}`),
			0o600,
		))

		cmd := &TestCmd{}
		opts := cmd.loadScenarioOptions(dir)
		assert.Equal(t, "3.11", opts["version"])
		assert.Equal(t, "true", opts["installTools"])
	})

	t.Run("missing scenario.json", func(t *testing.T) {
		dir := t.TempDir()
		cmd := &TestCmd{}
		opts := cmd.loadScenarioOptions(dir)
		assert.Nil(t, opts)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, "scenario.json"),
			[]byte(`not json`),
			0o600,
		))

		cmd := &TestCmd{}
		opts := cmd.loadScenarioOptions(dir)
		assert.Nil(t, opts)
	})
}

func TestTestCmd_TestDiscovery(t *testing.T) {
	projectDir := t.TempDir()

	srcDir := filepath.Join(projectDir, "src", "my-feature")
	require.NoError(t, os.MkdirAll(srcDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(srcDir, "devcontainer-feature.json"),
		[]byte(`{"id":"my-feature","version":"1.0.0","name":"My Feature"}`),
		0o600,
	))
	require.NoError(t, os.WriteFile( // #nosec G306 -- test scripts need executable permission
		filepath.Join(srcDir, "install.sh"),
		[]byte("#!/bin/bash\necho installed"),
		0o700,
	))

	testDir := filepath.Join(projectDir, "test", "my-feature")
	require.NoError(t, os.MkdirAll(testDir, 0o700))
	require.NoError(t, os.WriteFile( // #nosec G306 -- test scripts need executable permission
		filepath.Join(testDir, "test.sh"),
		[]byte("#!/bin/bash\necho test passed"),
		0o700,
	))

	scenarioDir := filepath.Join(testDir, "scenarios", "custom-options")
	require.NoError(t, os.MkdirAll(scenarioDir, 0o700))
	require.NoError(t, os.WriteFile( // #nosec G306 -- test scripts need executable permission
		filepath.Join(scenarioDir, "test.sh"),
		[]byte("#!/bin/bash\necho scenario test"),
		0o700,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(scenarioDir, "scenario.json"),
		[]byte(`{"options":{"version":"3.11"}}`),
		0o600,
	))

	feat := featureEntry{id: "my-feature", config: nil}
	cmd := &TestCmd{
		BaseImage:  defaultBaseImage,
		RemoteUser: "root",
	}

	featureTestDir := filepath.Join(projectDir, "test", feat.id)
	globalTestScript := filepath.Join(featureTestDir, "test.sh")

	_, err := os.Stat(globalTestScript)
	assert.NoError(t, err, "global test script should exist")

	scenariosPath := filepath.Join(featureTestDir, "scenarios")
	scenarios, err := os.ReadDir(scenariosPath)
	require.NoError(t, err)
	assert.Len(t, scenarios, 1)
	assert.Equal(t, "custom-options", scenarios[0].Name())

	opts := cmd.loadScenarioOptions(scenarioDir)
	assert.Equal(t, "3.11", opts["version"])

	df := strings.TrimSpace(cmd.generateDockerfile(feat, opts))
	assert.Contains(t, df, "FROM "+defaultBaseImage)
	assert.Contains(t, df, "MY-FEATURE_VERSION=3.11")
}
