package docker

import (
	"os"
	"path/filepath"
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
