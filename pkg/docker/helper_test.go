package docker

import (
	"context"
	"io"
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

func TestPull_PlatformArg(t *testing.T) {
	tmp := t.TempDir()
	argsFile := filepath.Join(tmp, "args.txt")
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
echo "$@" > `+argsFile+`
`)

	t.Run("includes --platform when set", func(t *testing.T) {
		h := &DockerHelper{DockerCommand: bin}
		require.NoError(t, h.Pull(context.Background(), PullOptions{
			Image:    "ubuntu:22.04",
			Platform: "linux/amd64",
			Stdout:   io.Discard,
			Stderr:   io.Discard,
		}))
		//nolint:gosec // test reads a temp file path it controls
		got, err := os.ReadFile(argsFile)
		require.NoError(t, err)
		assert.Equal(t, "pull --platform linux/amd64 ubuntu:22.04", strings.TrimSpace(string(got)))
	})

	t.Run("omits --platform when empty", func(t *testing.T) {
		h := &DockerHelper{DockerCommand: bin}
		require.NoError(t, h.Pull(context.Background(), PullOptions{
			Image:  "ubuntu:22.04",
			Stdout: io.Discard,
			Stderr: io.Discard,
		}))
		//nolint:gosec // test reads a temp file path it controls
		got, err := os.ReadFile(argsFile)
		require.NoError(t, err)
		assert.Equal(t, "pull ubuntu:22.04", strings.TrimSpace(string(got)))
	})
}

func TestFindContainerJSON_MatchesAllLabels(t *testing.T) {
	tmp := t.TempDir()
	// Fake docker: `ps -q -a` lists three containers; `inspect` returns each
	// container's labels. c1 matches both query labels; c2 matches only the
	// last label (an earlier label differs); c3 inspect returns an empty array.
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
case "$1" in
  ps) printf 'c1\nc2\nc3\n' ;;
  inspect)
    case "$4" in
      c1) echo '[{"ID":"c1","Config":{"Labels":{"a":"x","b":"y"}}}]' ;;
      c2) echo '[{"ID":"c2","Config":{"Labels":{"a":"zzz","b":"y"}}}]' ;;
      c3) echo '[]' ;;
    esac ;;
esac
`)

	h := &DockerHelper{DockerCommand: bin}
	got, err := h.FindContainerJSON(context.Background(), []string{"a=x", "b=y"})

	require.NoError(t, err)
	// Only c1 satisfies every label. c2 must be excluded (the AND-logic bug
	// previously matched it on the last label alone), and c3's empty inspect
	// result must not panic.
	assert.Equal(t, []string{"c1"}, got)
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
