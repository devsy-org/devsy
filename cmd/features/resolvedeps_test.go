package features

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveDepsCmd_NoFeatures(t *testing.T) {
	workspaceDir := t.TempDir()
	devcontainerDir := filepath.Join(workspaceDir, ".devcontainer")
	require.NoError(t, os.MkdirAll(devcontainerDir, 0o750))

	devcontainerJSON := `{
		"image": "ubuntu:22.04"
	}`
	require.NoError(t, os.WriteFile(
		filepath.Join(devcontainerDir, "devcontainer.json"),
		[]byte(devcontainerJSON),
		0o600,
	))

	cmd := &ResolveDepsCmd{
		WorkspaceFolder: workspaceDir,
		Output:          "text",
	}

	err := cmd.Run()
	require.NoError(t, err)
}

func TestResolveDepsCmd_MissingWorkspace(t *testing.T) {
	cmd := &ResolveDepsCmd{
		WorkspaceFolder: "/nonexistent/path/12345",
		Output:          "text",
	}

	err := cmd.Run()
	assert.Error(t, err)
}

func TestResolveDepsCmd_ExplicitConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "devcontainer.json")

	devcontainerJSON := `{
		"image": "ubuntu:22.04",
		"features": {}
	}`
	require.NoError(t, os.WriteFile(configPath, []byte(devcontainerJSON), 0o600))

	cmd := &ResolveDepsCmd{
		WorkspaceFolder: tmpDir,
		Config:          configPath,
		Output:          outputJSON,
	}

	err := cmd.Run()
	require.NoError(t, err)
}
