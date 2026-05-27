package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeScript(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	//nolint:gosec // test helper script needs exec bit
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

func TestGPUSupportEnabled_DockerWithNvidia(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
case "$1" in
  --version) echo "Docker version 24.0.7, build afdd53b";;
  info) echo "nvidia-container-runtime";;
esac
`)

	h := &DockerHelper{DockerCommand: bin}
	got, err := h.GPUSupportEnabled()

	assert.NoError(t, err)
	assert.True(t, got, "should detect GPU when Docker nvidia runtime is present")
}

func TestGPUSupportEnabled_DockerWithoutNvidia(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
case "$1" in
  --version) echo "Docker version 24.0.7, build afdd53b";;
  info) echo "{}";;
esac
`)

	h := &DockerHelper{DockerCommand: bin}
	got, err := h.GPUSupportEnabled()

	assert.NoError(t, err)
	assert.False(t, got, "should not detect GPU when Docker nvidia runtime is absent")
}

func TestGPUSupportEnabled_PodmanWithCDINvidia(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "podman-fake", `#!/bin/sh
case "$1" in
  --version) echo "podman version 4.9.3";;
  info) echo "[nvidia.com/gpu=all]";;
esac
`)

	h := &DockerHelper{DockerCommand: bin}
	got, err := h.GPUSupportEnabled()

	assert.NoError(t, err)
	assert.True(t, got, "should detect GPU when Podman CDI has nvidia device")
}

func TestGPUSupportEnabled_PodmanWithoutCDINvidia(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "podman-fake", `#!/bin/sh
case "$1" in
  --version) echo "podman version 4.9.3";;
  info) echo "[]";;
esac
`)

	h := &DockerHelper{DockerCommand: bin}
	got, err := h.GPUSupportEnabled()

	assert.NoError(t, err)
	assert.False(t, got, "should not detect GPU when Podman CDI has no nvidia device")
}

func TestCreateVolume_PassesLabelsAndName(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "args.log")
	script := `#!/bin/sh
echo "$@" > ` + logPath + `
echo created
`
	bin := writeScript(t, tmp, "docker-fake", script)

	h := &DockerHelper{DockerCommand: bin}
	err := h.CreateVolume(context.Background(), "vol-1", map[string]string{
		LabelDriverOwned: "true",
	})
	require.NoError(t, err)

	//nolint:gosec // logPath is a test-controlled temp file
	logged, err := os.ReadFile(logPath)
	require.NoError(t, err)
	got := strings.TrimSpace(string(logged))
	assert.Contains(t, got, "volume create")
	assert.Contains(t, got, "--label "+LabelDriverOwned+"=true")
	assert.Contains(t, got, "vol-1")
}

func TestCreateVolume_AlreadyExistsIsOK(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
echo "Error response from daemon: a volume with that name already exists" 1>&2
exit 1
`)

	h := &DockerHelper{DockerCommand: bin}
	err := h.CreateVolume(context.Background(), "vol-1", nil)
	assert.NoError(t, err)
}

func TestCreateVolume_EmptyNameRejected(t *testing.T) {
	h := &DockerHelper{DockerCommand: "/bin/true"}
	err := h.CreateVolume(context.Background(), "", nil)
	assert.Error(t, err)
}

func TestInspectVolumeLabels_NoSuchVolume(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
echo "Error: no such volume: vol-x" 1>&2
exit 1
`)

	h := &DockerHelper{DockerCommand: bin}
	labels, err := h.InspectVolumeLabels(context.Background(), "vol-x")
	assert.NoError(t, err)
	assert.Nil(t, labels)
}

func TestInspectVolumeLabels_ParsesJSON(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
echo '{"devsy.driver-owned":"true","devsy.workspace-id":"abc"}'
`)

	h := &DockerHelper{DockerCommand: bin}
	labels, err := h.InspectVolumeLabels(context.Background(), "vol")
	require.NoError(t, err)
	assert.Equal(t, "true", labels[LabelDriverOwned])
	assert.Equal(t, "abc", labels[LabelWorkspaceID])
}

func TestInspectVolumeLabels_NullLabels(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
echo null
`)

	h := &DockerHelper{DockerCommand: bin}
	labels, err := h.InspectVolumeLabels(context.Background(), "vol")
	require.NoError(t, err)
	assert.Empty(t, labels)
}

func TestGPUSupportEnabled_CommandFailure(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "bad-runtime", `#!/bin/sh
exit 1
`)

	h := &DockerHelper{DockerCommand: bin}
	got, err := h.GPUSupportEnabled()

	assert.NoError(t, err, "should not propagate error on command failure")
	assert.False(t, got, "should fall back to no GPU on command failure")
}
