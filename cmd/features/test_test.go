package features

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTestCmd_FlagDefaults(t *testing.T) {
	cmd := NewTestCmd(nil)

	baseImageFlag := cmd.Flags().Lookup("base-image")
	require.NotNil(t, baseImageFlag)
	assert.Equal(t, "mcr.microsoft.com/devcontainers/base:ubuntu", baseImageFlag.DefValue)

	remoteUserFlag := cmd.Flags().Lookup("remote-user")
	require.NotNil(t, remoteUserFlag)
	assert.Equal(t, "root", remoteUserFlag.DefValue)

	outputFlag := cmd.Flags().Lookup("output")
	require.NotNil(t, outputFlag)
	assert.Equal(t, "text", outputFlag.DefValue)

	skipScenariosFlag := cmd.Flags().Lookup("skip-scenarios")
	require.NotNil(t, skipScenariosFlag)
	assert.Equal(t, "false", skipScenariosFlag.DefValue)
}

func TestTestCmd_AllFlagsRegistered(t *testing.T) {
	cmd := NewTestCmd(nil)
	expected := []string{"project-folder", "base-image", "remote-user", "output", "skip-scenarios"}
	for _, name := range expected {
		assert.NotNil(t, cmd.Flags().Lookup(name), "flag %q should be registered", name)
	}
}

func TestTestCmd_InvalidOutputFormat(t *testing.T) {
	testCmd := &TestCmd{
		Output:        "yaml",
		ProjectFolder: "/nonexistent",
	}
	err := testCmd.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid output format")
}

func TestTestCmd_MissingFeatureJSON(t *testing.T) {
	tmpDir := t.TempDir()

	testCmd := &TestCmd{
		Output:        "text",
		ProjectFolder: tmpDir,
		BaseImage:     "ubuntu:22.04",
		RemoteUser:    "root",
	}
	err := testCmd.Run(t.Context())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse feature metadata")
}

func TestGenerateTestDockerfile_Basic(t *testing.T) {
	featureCfg := &config.FeatureConfig{
		ID:      "go",
		Version: "1.0.0",
		Name:    "Go",
	}

	got := generateTestDockerfile("ubuntu:22.04", featureCfg, "root")

	assert.Contains(t, got, "FROM ubuntu:22.04")
	assert.Contains(t, got, "USER root")
	assert.Contains(t, got, "COPY feature/ /tmp/_dev_container_feature/")
	assert.Contains(t, got, "RUN chmod +x install.sh && ./install.sh")
	assert.Contains(t, got, "RUN rm -rf /tmp/_dev_container_feature")
}

func TestGenerateTestDockerfile_WithOptions(t *testing.T) {
	featureCfg := &config.FeatureConfig{
		ID:      "node",
		Version: "1.0.0",
		Options: map[string]config.FeatureConfigOption{
			"version": {
				Type:    "string",
				Default: types.StrBool("18"),
			},
			"installYarn": {
				Type:    "boolean",
				Default: types.StrBool("true"),
			},
		},
	}

	got := generateTestDockerfile("ubuntu:22.04", featureCfg, "vscode")

	assert.Contains(t, got, "USER vscode")
	assert.Contains(t, got, "ENV VERSION=18")
	assert.Contains(t, got, "ENV INSTALLYARN=true")
}

func TestTestCmd_RunTestScenarios_NoTestDir(t *testing.T) {
	tmpDir := t.TempDir()

	testCmd := &TestCmd{
		RemoteUser: "root",
	}
	results, err := testCmd.runTestScenarios(t.Context(), "fake-container-id", tmpDir)
	require.NoError(t, err)
	assert.Nil(t, results)
}

func TestTestCmd_RunTestScenarios_SkipsNonSh(t *testing.T) {
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "test")
	require.NoError(t, os.MkdirAll(testDir, 0o750))
	require.NoError(t, os.WriteFile(
		filepath.Join(testDir, "readme.md"), []byte("# Tests"), 0o600,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(testDir, "subdir"), 0o750))

	testCmd := &TestCmd{
		RemoteUser: "root",
	}
	results, err := testCmd.runTestScenarios(t.Context(), "fake-container-id", tmpDir)
	require.NoError(t, err)
	assert.Empty(t, results)
}
