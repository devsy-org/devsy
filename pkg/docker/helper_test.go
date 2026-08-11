package docker

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testFakeCommand = "cmd"

var testExecArgs = []string{"exec", "c1", testFakeCommand}

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
	assert.Equal(t, []string{"c1"}, got)
}

func TestWaitContainerRunning_RunningSucceeds(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
case "$1" in
  inspect) echo '[{"ID":"c1","State":{"Status":"running"}}]' ;;
esac
`)

	h := &DockerHelper{DockerCommand: bin}
	require.NoError(t, h.WaitContainerRunning(context.Background(), "c1"))
}

func TestWaitContainerRunning_ExitedFailsFast(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
case "$1" in
  inspect) echo '[{"ID":"c1","State":{"Status":"exited","ExitCode":137,"Error":"OOM"}}]' ;;
  logs) echo 'boom' ;;
esac
`)

	h := &DockerHelper{DockerCommand: bin}
	start := time.Now()
	err := h.WaitContainerRunning(context.Background(), "c1")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrContainerExited), "expected exited sentinel, got %v", err)
	assert.False(t, errors.Is(err, ErrContainerTerminal), "exited must not be terminal")
	assert.Contains(t, err.Error(), "137")
	assert.Contains(t, err.Error(), "OOM")
	assert.Less(t, time.Since(start), 10*time.Second)
}

func TestWaitContainerRunning_DeadFailsFast(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
case "$1" in
  inspect) echo '[{"ID":"c1","State":{"Status":"dead","ExitCode":1}}]' ;;
  logs) echo '' ;;
esac
`)

	h := &DockerHelper{DockerCommand: bin}
	err := h.WaitContainerRunning(context.Background(), "c1")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrContainerTerminal), "expected terminal sentinel, got %v", err)
}

func TestIsImageNotFoundError(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		want bool
	}{
		{"docker no such image", "Error response from daemon: No such image: foo:bar", true},
		{"podman no such object", "Error: no such object: foo", true},
		{"nerdctl image not known", "image not known", true},
		{"remote manifest unknown", "MANIFEST_UNKNOWN: manifest unknown", true},
		{"registry name unknown", "NAME_UNKNOWN: name unknown", true},
		{"registry repository not known", "repository name not known to registry", true},
		{"daemon unreachable", "Cannot connect to the Docker daemon at the socket", false},
		{"permission denied", "permission denied while trying to connect", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isImageNotFoundError(errors.New(tt.msg)))
		})
	}
}

func TestInspectImage_NotFoundReturnsSentinel(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
echo 'Error response from daemon: No such image: foo:bar' >&2
exit 1
`)

	h := &DockerHelper{DockerCommand: bin}
	details, err := h.InspectImage(context.Background(), "foo:bar", false)

	require.Error(t, err)
	assert.Nil(t, details)
	assert.True(t, errors.Is(err, ErrImageNotFound), "expected ErrImageNotFound, got %v", err)
}

func TestInspectImage_RealErrorNotMistakenForMiss(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
echo 'Cannot connect to the Docker daemon at unix:///var/run/docker.sock' >&2
exit 1
`)

	h := &DockerHelper{DockerCommand: bin}
	details, err := h.InspectImage(context.Background(), "foo:bar", false)

	require.Error(t, err)
	assert.Nil(t, details)
	assert.False(
		t,
		errors.Is(err, ErrImageNotFound),
		"daemon failure must not be reported as a miss",
	)
}

func TestInspectImage_EmptyResultReturnsSentinel(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
echo '[]'
`)

	h := &DockerHelper{DockerCommand: bin}
	details, err := h.InspectImage(context.Background(), "foo:bar", false)

	require.Error(t, err)
	assert.Nil(t, details)
	assert.True(t, errors.Is(err, ErrImageNotFound), "empty inspect result should be a miss")
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

func TestGetImageTag(t *testing.T) {
	tests := []struct {
		name    string
		repoTag string
		want    string
	}{
		{"normal tag", "ubuntu:22.04\n", "22.04"},
		{"registry port", "localhost:5000/acme/repo:tag\n", "tag"},
		{"registry with path", "ghcr.io/devsy-org/base:ubuntu\n", "ubuntu"},
		{"digest dropped", "localhost:5000/acme/repo:tag@sha256:abc\n", "tag"},
		{"no repo tags", "", ""},
		{"reference without tag", "alpine\n", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmp := t.TempDir()
			outFile := filepath.Join(tmp, "repotag")
			require.NoError(t, os.WriteFile(outFile, []byte(tt.repoTag), 0o600))
			bin := writeScript(t, tmp, "docker-fake", "#!/bin/sh\ncat "+outFile+"\n")
			h := &DockerHelper{DockerCommand: bin}
			got, err := h.GetImageTag(context.Background(), "img")
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunCmd_AttachesCtxErrOnFailure(t *testing.T) {
	tmp := t.TempDir()
	ready := filepath.Join(tmp, "ready")
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
touch `+ready+`
exec sleep 30
`)
	h := &DockerHelper{DockerCommand: bin}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(ready); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()

	streams := Streams{Stdout: io.Discard, Stderr: io.Discard}
	err := h.Run(ctx, testExecArgs, streams)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRunCmd_AlreadyCancelledBeforeStart(t *testing.T) {
	bin := writeScript(t, t.TempDir(), "docker-fake", `#!/bin/sh
exit 1
`)
	h := &DockerHelper{DockerCommand: bin}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	streams := Streams{Stdout: io.Discard, Stderr: io.Discard}
	err := h.Run(ctx, testExecArgs, streams)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRunCmd_NoCtxErrWhenNotCancelled(t *testing.T) {
	bin := writeScript(t, t.TempDir(), "docker-fake", `#!/bin/sh
exit 1
`)
	h := &DockerHelper{DockerCommand: bin}

	streams := Streams{Stdout: io.Discard, Stderr: io.Discard}
	err := h.Run(context.Background(), testExecArgs, streams)

	require.Error(t, err)
	assert.NotErrorIs(t, err, context.Canceled)
}

func TestRunCmd_CancelAfterReturnNotRetroactivelyAttributed(t *testing.T) {
	bin := writeScript(t, t.TempDir(), "docker-fake", `#!/bin/sh
exit 1
`)
	h := &DockerHelper{DockerCommand: bin}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	streams := Streams{Stdout: io.Discard, Stderr: io.Discard}
	err := h.Run(ctx, testExecArgs, streams)
	cancel()

	require.Error(t, err)
	assert.NotErrorIs(t, err, context.Canceled)
}

func TestDeleteVolume_EmptyNameIsNoOp(t *testing.T) {
	h := &DockerHelper{DockerCommand: "/nonexistent-binary-xyz"}
	assert.NoError(t, h.DeleteVolume(context.Background(), ""))
}

func TestDeleteVolume_MissingVolumeIsNoOp(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
case "$1" in
  volume)
    case "$2" in
      list) ;; # no matching volume -> empty stdout, exit 0
      rm) echo "unexpected rm call" >&2; exit 1 ;;
    esac ;;
esac
`)

	h := &DockerHelper{DockerCommand: bin}
	assert.NoError(t, h.DeleteVolume(context.Background(), "ghost-volume"))
}

func TestDeleteVolume_DaemonUnreachablePropagatesError(t *testing.T) {
	tmp := t.TempDir()
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
case "$1" in
  volume)
    case "$2" in
      list) echo "Cannot connect to the Docker daemon at unix:///var/run/docker.sock" >&2; exit 1 ;;
    esac ;;
esac
`)

	h := &DockerHelper{DockerCommand: bin}
	err := h.DeleteVolume(context.Background(), "my-volume")

	require.Error(t, err, "daemon failure during volume list must not be swallowed as a miss")
	assert.Contains(t, err.Error(), "list volume my-volume")
	assert.Contains(t, err.Error(), "Cannot connect to the Docker daemon")
}

func TestDeleteVolume_RemovesExistingVolume(t *testing.T) {
	tmp := t.TempDir()
	removed := filepath.Join(tmp, "removed")
	bin := writeScript(t, tmp, "docker-fake", `#!/bin/sh
case "$1" in
  volume)
    case "$2" in
      list) echo "my-volume" ;;
      rm) touch `+removed+` ;;
    esac ;;
esac
`)

	h := &DockerHelper{DockerCommand: bin}
	require.NoError(t, h.DeleteVolume(context.Background(), "my-volume"))
	_, err := os.Stat(removed)
	assert.NoError(t, err, "volume rm should be invoked for an existing volume")
}
