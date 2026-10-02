package dockercredentials

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPreserveDockerConfig_ContextMetadata(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	require.NoError(t, os.MkdirAll(srcDir, 0o750))

	t.Setenv("DOCKER_CONFIG", srcDir)

	configContent := []byte(`{"currentContext":"desktop-linux","HttpHeaders":{"X-Custom":"true"}}`)
	configFile := filepath.Join(srcDir, "config.json")
	require.NoError(t, os.WriteFile(configFile, configContent, 0o600))

	metaContent := []byte(`{"Endpoints":{"docker":{"Host":"unix:///custom/docker.sock"}}}`)
	metaDir := filepath.Join(srcDir, "contexts", "meta", "some-id")
	require.NoError(t, os.MkdirAll(metaDir, 0o750))
	metaFile := filepath.Join(metaDir, "meta.json")
	require.NoError(t, os.WriteFile(metaFile, metaContent, 0o600))

	destDir := filepath.Join(tempDir, "dest")
	require.NoError(t, preserveDockerConfig(destDir))

	configBytes, err := os.ReadFile(filepath.Clean(filepath.Join(destDir, "config.json")))
	require.NoError(t, err)
	assert.Equal(t, configContent, configBytes)

	destMetaFile := filepath.Clean(
		filepath.Join(destDir, "contexts", "meta", "some-id", "meta.json"),
	)
	destMetaBytes, err := os.ReadFile(destMetaFile)
	require.NoError(t, err)
	assert.Equal(t, metaContent, destMetaBytes)
}

func TestPreserveDockerConfig_EmptySource(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	t.Setenv("DOCKER_CONFIG", srcDir)

	destDir := filepath.Join(tempDir, "dest")
	err := preserveDockerConfig(destDir)
	require.NoError(t, err)

	_, err = os.Stat(destDir)
	require.NoError(t, err)
}

func TestPreserveDockerConfig_CLIPluginDirectory(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	pluginDir := filepath.Join(srcDir, "cli-plugins")
	require.NoError(t, os.MkdirAll(pluginDir, 0o750))
	t.Setenv("DOCKER_CONFIG", srcDir)

	configFile := filepath.Join(srcDir, "config.json")
	require.NoError(t, os.WriteFile(configFile, []byte(
		`{"currentContext":"test","cliPluginsExtraDirs":["/existing/plugins"]}`,
	), 0o600))

	destDir := filepath.Join(tempDir, "dest")
	require.NoError(t, preserveDockerConfig(destDir))

	configPath := filepath.Join(destDir, "config.json")
	contents, err := os.ReadFile(configPath) // #nosec G304 -- test path is confined to t.TempDir().
	require.NoError(t, err)
	var got struct {
		CurrentContext      string   `json:"currentContext"`
		CLIPluginsExtraDirs []string `json:"cliPluginsExtraDirs"`
	}
	require.NoError(t, json.Unmarshal(contents, &got))
	assert.Equal(t, "test", got.CurrentContext)
	assert.Equal(t, []string{"/existing/plugins", pluginDir}, got.CLIPluginsExtraDirs)
}

func TestPreserveDockerConfig_CLIPluginDirectoryWithoutExistingConfig(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src")
	pluginDir := filepath.Join(srcDir, "cli-plugins")
	require.NoError(t, os.MkdirAll(pluginDir, 0o750))
	t.Setenv("DOCKER_CONFIG", srcDir)

	configFile := filepath.Join(srcDir, "config.json")
	require.NoError(t, os.WriteFile(configFile, []byte(`{"currentContext":"test"}`), 0o600))
	destDir := filepath.Join(tempDir, "dest")
	require.NoError(t, preserveDockerConfig(destDir))

	configPath := filepath.Join(destDir, "config.json")
	contents, err := os.ReadFile(configPath) // #nosec G304 -- test path is confined to t.TempDir().
	require.NoError(t, err)
	var got struct {
		CurrentContext      string   `json:"currentContext"`
		CLIPluginsExtraDirs []string `json:"cliPluginsExtraDirs"`
	}
	require.NoError(t, json.Unmarshal(contents, &got))
	assert.Equal(t, "test", got.CurrentContext)
	assert.Equal(t, []string{pluginDir}, got.CLIPluginsExtraDirs)
}

func TestPreserveDockerConfig_InaccessibleSource(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "inaccessible-src")
	require.NoError(t, os.MkdirAll(srcDir, 0o750))

	configFile := filepath.Join(srcDir, "config.json")
	require.NoError(t, os.Symlink(configFile, configFile))

	t.Setenv("DOCKER_CONFIG", srcDir)

	destDir := filepath.Join(tempDir, "dest")
	err := preserveDockerConfig(destDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspect Docker config")
}

func TestPreserveDockerConfig_InaccessibleContexts(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src-file-contexts")
	require.NoError(t, os.MkdirAll(srcDir, 0o750))
	t.Setenv("DOCKER_CONFIG", srcDir)

	contextsPath := filepath.Join(srcDir, "contexts")
	require.NoError(t, os.WriteFile(contextsPath, []byte("not-a-directory"), 0o600))

	destDir := filepath.Join(tempDir, "dest")
	err := preserveDockerConfig(destDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "copy contexts")
}

func TestPreserveDockerConfig_StatErrorContexts(t *testing.T) {
	tempDir := t.TempDir()
	srcDir := filepath.Join(tempDir, "src-loop-contexts")
	require.NoError(t, os.MkdirAll(srcDir, 0o750))
	t.Setenv("DOCKER_CONFIG", srcDir)

	contextsDir := filepath.Join(srcDir, "contexts")
	require.NoError(t, os.Symlink(contextsDir, contextsDir))

	destDir := filepath.Join(tempDir, "dest")
	err := preserveDockerConfig(destDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "inspect Docker contexts")
}
