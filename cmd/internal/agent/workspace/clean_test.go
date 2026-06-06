package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanCmd_EmptyWorkspaceID(t *testing.T) {
	cmd := &CleanCmd{GlobalFlags: &flags.GlobalFlags{}}
	err := cmd.Run(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestCleanCmd_VolumeNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	script := "#!/bin/sh\necho 'no such volume' >&2; exit 1\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	cmd := &CleanCmd{
		GlobalFlags:   &flags.GlobalFlags{},
		DockerCommand: scriptPath,
	}
	err := cmd.Run(context.Background(), "nonexistent-ws")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestCleanCmd_RemoveBinarySuccess(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	markerPath := filepath.Join(tmpDir, "rm-called")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  volume) echo '{}' ;;\n" +
		"  run) touch \"" + markerPath + "\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	cmd := &CleanCmd{
		GlobalFlags:   &flags.GlobalFlags{},
		DockerCommand: scriptPath,
	}
	err := cmd.Run(context.Background(), "test-workspace-123")
	require.NoError(t, err)

	_, statErr := os.Stat(markerPath)
	assert.NoError(t, statErr, "docker run should have been called to remove the binary")
}

func TestCleanCmd_DockerRunFails(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  volume) echo '{}' ;;\n" +
		"  run) echo 'container error' >&2; exit 1 ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	cmd := &CleanCmd{
		GlobalFlags:   &flags.GlobalFlags{},
		DockerCommand: scriptPath,
	}
	err := cmd.Run(context.Background(), "test-workspace")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "docker run failed")
}

func TestCleanCmd_VolumeName(t *testing.T) {
	assert.Equal(t, "devsy-agent-", cleanVolumePrefix)
	assert.Equal(t, "devsy-agent-my-ws", cleanVolumePrefix+"my-ws")
}

func TestCleanCmd_HelperImage_Default(t *testing.T) {
	cmd := &CleanCmd{GlobalFlags: &flags.GlobalFlags{}}
	assert.Equal(t, "busybox:latest", cmd.helperImage())
}

func TestCleanCmd_HelperImage_Custom(t *testing.T) {
	cmd := &CleanCmd{
		GlobalFlags: &flags.GlobalFlags{},
		HelperImage: "alpine:latest",
	}
	assert.Equal(t, "alpine:latest", cmd.helperImage())
}

func TestCleanCmd_DockerCommand_Default(t *testing.T) {
	cmd := &CleanCmd{GlobalFlags: &flags.GlobalFlags{}}
	assert.Equal(t, "docker", cmd.dockerCommand())
}

func TestCleanCmd_DockerCommand_Custom(t *testing.T) {
	cmd := &CleanCmd{
		GlobalFlags:   &flags.GlobalFlags{},
		DockerCommand: "podman",
	}
	assert.Equal(t, "podman", cmd.dockerCommand())
}

func TestNewCleanCmd_CobraSetup(t *testing.T) {
	cobraCmd := NewCleanCmd(&flags.GlobalFlags{})
	assert.Equal(t, "clean [workspace-id]", cobraCmd.Use)
	assert.NotEmpty(t, cobraCmd.Short)
	assert.NotEmpty(t, cobraCmd.Long)
}
