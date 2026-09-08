package dockercredentials

import (
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
