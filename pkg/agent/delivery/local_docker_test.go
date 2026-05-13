package delivery

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalDockerDelivery_Phase(t *testing.T) {
	d := &LocalDockerDelivery{}
	assert.Equal(t, PhasePreStart, d.Phase())
}

func TestLocalDockerDelivery_DeliverPreStart_RequiresBinarySource(t *testing.T) {
	d := &LocalDockerDelivery{}
	err := d.DeliverPreStart(context.Background(), PreStartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "binary source is required")
}

func TestLocalDockerDelivery_DeliverPostStart_ReturnsError(t *testing.T) {
	d := &LocalDockerDelivery{}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support post-start")
}

func TestDeliveryPhase_String(t *testing.T) {
	assert.Equal(t, "pre-start", PhasePreStart.String())
	assert.Equal(t, "post-start", PhasePostStart.String())
	assert.Contains(t, DeliveryPhase(99).String(), "unknown")
}

func TestBinaryName(t *testing.T) {
	name := binaryName()
	assert.Equal(t, "devsy", name)
}

func TestLocalDockerDelivery_HelperImageName_Default(t *testing.T) {
	d := &LocalDockerDelivery{}
	assert.Equal(t, "busybox:latest", d.helperImageName())
}

func TestLocalDockerDelivery_HelperImageName_Configured(t *testing.T) {
	d := &LocalDockerDelivery{HelperImage: "registry.internal/tools/busybox:1.36"}
	assert.Equal(t, "registry.internal/tools/busybox:1.36", d.helperImageName())
}

func TestNewAgentDelivery_LocalDocker_ThreadsHelperImage(t *testing.T) {
	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{
				Driver: provider.DockerDriver,
			},
		},
		DockerCommand: defaultDockerCmd,
		HelperImage:   "my-registry/busybox:1.35",
	}

	d := NewAgentDelivery(opts)
	local, ok := d.(*LocalDockerDelivery)
	require.True(t, ok)
	assert.Equal(t, "my-registry/busybox:1.35", local.HelperImage)
}

func TestNewAgentDelivery_LocalDocker_EmptyHelperImage(t *testing.T) {
	opts := FactoryOptions{
		WorkspaceConfig: &provider.AgentWorkspaceInfo{
			Agent: provider.ProviderAgentConfig{
				Driver: provider.DockerDriver,
			},
		},
		DockerCommand: defaultDockerCmd,
	}

	d := NewAgentDelivery(opts)
	local, ok := d.(*LocalDockerDelivery)
	require.True(t, ok)
	assert.Empty(t, local.HelperImage)
	assert.Equal(t, "busybox:latest", local.helperImageName())
}

func TestPopulateVolume_FallbackToDirectCopy(t *testing.T) {
	tmpDir := t.TempDir()
	mountDir := filepath.Join(tmpDir, "mount")
	require.NoError(t, os.MkdirAll(mountDir, 0o750))

	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  run) echo \"image not found\" >&2; exit 1 ;;\n" +
		"  volume) echo \"" + mountDir + "\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	binaryContent := []byte("fake-agent-binary-content")
	binarySource := func(_ context.Context, _ string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(binaryContent)), nil
	}

	d := &LocalDockerDelivery{
		DockerCommand: scriptPath,
	}

	err := d.populateVolume(context.Background(), "test-vol", binarySource, "amd64")
	require.NoError(t, err)

	destPath := filepath.Join(mountDir, binaryName())
	data, err := os.ReadFile(destPath) //nolint:gosec // test reads from a temp directory we control
	require.NoError(t, err)
	assert.Equal(t, binaryContent, data)

	info, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestIsPodman(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{"default docker", "", false},
		{"explicit docker", "docker", false},
		{"explicit podman", podmanCmd, true},
		{"full path podman", "/usr/bin/podman", true},
		{"full path docker", "/usr/bin/docker", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &LocalDockerDelivery{DockerCommand: tt.cmd}
			assert.Equal(t, tt.want, d.isPodman())
		})
	}
}

func TestPopulateVolumeDirectCopy_PodmanUsesUnshare(t *testing.T) {
	tmpDir := t.TempDir()
	mountDir := filepath.Join(tmpDir, "mount")
	require.NoError(t, os.MkdirAll(mountDir, 0o750))

	destPath := filepath.Join(mountDir, binaryName())
	binaryContent := []byte("fake-agent-binary-content")

	scriptPath := filepath.Join(tmpDir, "podman")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  unshare) shift; exec \"$@\" ;;\n" +
		"  volume) echo \"" + mountDir + "\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	d := &LocalDockerDelivery{DockerCommand: scriptPath}
	err := d.populateVolumeDirectCopy(context.Background(), "test-vol", binaryContent)
	require.NoError(t, err)

	data, err := os.ReadFile(destPath) //nolint:gosec // test reads from a temp directory we control
	require.NoError(t, err)
	assert.Equal(t, binaryContent, data)

	info, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestPopulateVolumeDirectCopy_DockerUsesDirectWrite(t *testing.T) {
	tmpDir := t.TempDir()
	mountDir := filepath.Join(tmpDir, "mount")
	require.NoError(t, os.MkdirAll(mountDir, 0o750))

	binaryContent := []byte("fake-agent-binary-content")

	scriptPath := filepath.Join(tmpDir, "fake-docker.sh")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  volume) echo \"" + mountDir + "\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	d := &LocalDockerDelivery{DockerCommand: scriptPath}
	err := d.populateVolumeDirectCopy(context.Background(), "test-vol", binaryContent)
	require.NoError(t, err)

	destPath := filepath.Join(mountDir, binaryName())
	data, err := os.ReadFile(destPath) //nolint:gosec // test reads from a temp directory we control
	require.NoError(t, err)
	assert.Equal(t, binaryContent, data)

	info, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestPopulateVolumeViaUnshare_FailureReturnsError(t *testing.T) {
	tmpDir := t.TempDir()

	scriptPath := filepath.Join(tmpDir, "podman")
	script := "#!/bin/sh\necho 'unshare failed: permission denied' >&2; exit 1\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o600))
	// #nosec G302 -- test script must be executable
	require.NoError(t, os.Chmod(scriptPath, 0o755))

	d := &LocalDockerDelivery{DockerCommand: scriptPath}
	err := d.populateVolumeViaUnshare(context.Background(), "/fake/path/devsy", []byte("data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "podman unshare write failed")
}
