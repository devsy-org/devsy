package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/subprocess"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testImageRef = "x:latest"

func fakeDocker(t *testing.T, startsUntilRunning int) string {
	t.Helper()
	dir := t.TempDir()
	state := filepath.Join(dir, "starts")
	script := `#!/bin/sh
STATE="` + state + `"
THRESHOLD=` + strconv.Itoa(startsUntilRunning) + `
case "$1" in
  start)
    n=$(cat "$STATE" 2>/dev/null || echo 0)
    n=$((n + 1))
    echo "$n" > "$STATE"
    echo started
    ;;
  inspect)
    n=$(cat "$STATE" 2>/dev/null || echo 0)
    if [ "$THRESHOLD" -ge 0 ] && [ "$n" -ge "$THRESHOLD" ]; then
      echo '[{"ID":"c1","State":{"Status":"running"}}]'
    else
      echo '[{"ID":"c1","State":{"Status":"exited","ExitCode":1}}]'
    fi
    ;;
  logs) echo 'boot failed' ;;
esac
`
	path := filepath.Join(dir, "docker-fake")
	//nolint:gosec // test helper script needs exec bit
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

func newExitedContainer() *config.ContainerDetails {
	return &config.ContainerDetails{
		ID:    "c1",
		State: config.ContainerDetailsState{Status: "exited"},
	}
}

// fakeEnsureImageDocker emulates `docker inspect` failing with the given
// stderr and records whether `docker pull` was invoked by touching marker.
func fakeEnsureImageDocker(t *testing.T, inspectStderr, marker string) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
case "$1" in
  inspect)
    echo '` + inspectStderr + `' >&2
    exit 1
    ;;
  pull)
    echo pulled > "` + marker + `"
    ;;
esac
`
	path := filepath.Join(dir, "docker-fake")
	//nolint:gosec // test helper script needs exec bit
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return path
}

func withShortImageInspectPoll(t *testing.T) {
	t.Helper()
	origInterval, origMaxAttempts := imageInspectPollInterval, imageInspectPollMaxAttempts
	imageInspectPollInterval = 5 * time.Millisecond
	imageInspectPollMaxAttempts = 2
	t.Cleanup(func() {
		imageInspectPollInterval, imageInspectPollMaxAttempts = origInterval, origMaxAttempts
	})
}

func TestEnsureImage_BuiltImageNeverFoundDoesNotPull(t *testing.T) {
	withShortImageInspectPoll(t)
	marker := filepath.Join(t.TempDir(), "pulled")
	bin := fakeEnsureImageDocker(t, "Error response from daemon: No such image: x", marker)
	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}

	err := d.EnsureImage(
		context.Background(),
		&driver.RunOptions{Image: testImageRef, ImageBuilt: true},
	)

	require.Error(
		t,
		err,
		"a devsy-built image that never appears locally must not fall back to pull",
	)
	assert.ErrorIs(t, err, docker.ErrImageNotFound)
	assert.NoFileExists(
		t,
		marker,
		"a locally-built image can never be resolved by a pull; pulling risks running an unrelated image under the same tag",
	)
}

func TestEnsureImage_TransientMissRecoversWithoutPulling(t *testing.T) {
	withShortImageInspectPoll(t)
	imageInspectPollMaxAttempts = 5 // must outlast a couple of retries

	tmp := t.TempDir()
	marker := filepath.Join(tmp, "pulled")
	readyAfter := filepath.Join(tmp, "attempts")
	bin := filepath.Join(tmp, "docker-fake")
	script := `#!/bin/sh
case "$1" in
  inspect)
    n=$(cat "` + readyAfter + `" 2>/dev/null || echo 0)
    n=$((n + 1))
    echo "$n" > "` + readyAfter + `"
    if [ "$n" -lt 3 ]; then
      echo 'Error response from daemon: No such image: x' >&2
      exit 1
    fi
    echo '[{"Id":"sha256:fake"}]'
    ;;
  pull)
    echo pulled > "` + marker + `"
    ;;
esac
`
	//nolint:gosec // test helper script needs exec bit
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755))
	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}

	err := d.EnsureImage(
		context.Background(),
		&driver.RunOptions{Image: testImageRef, ImageBuilt: true},
	)

	require.NoError(t, err)
	assert.NoFileExists(t, marker,
		"a transient miss that resolves on retry must not fall back to pull")
}

func TestWaitForLocalImage_BoundsEachInspectAttempt(t *testing.T) {
	origTimeout := imageInspectAttemptTimeout
	imageInspectAttemptTimeout = 100 * time.Millisecond
	t.Cleanup(func() {
		imageInspectAttemptTimeout = origTimeout
	})

	dir := t.TempDir()
	bin := filepath.Join(dir, "docker-fake")
	script := `#!/bin/sh
case "$1" in
  inspect) sleep 5 ;;
esac
`
	//nolint:gosec // test helper script needs exec bit
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755))
	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}

	start := time.Now()
	err := d.waitForLocalImage(context.Background(), testImageRef)

	require.Error(t, err)
	assert.Less(t, time.Since(start), 2*time.Second)
}

func TestEnsureImage_ExternalImageSkipsRetryAndPullsImmediately(t *testing.T) {
	withShortImageInspectPoll(t)

	tmp := t.TempDir()
	marker := filepath.Join(tmp, "pulled")
	attempts := filepath.Join(tmp, "attempts")
	bin := filepath.Join(tmp, "docker-fake")
	script := `#!/bin/sh
case "$1" in
  inspect)
    n=$(cat "` + attempts + `" 2>/dev/null || echo 0)
    echo $((n + 1)) > "` + attempts + `"
    echo 'Error response from daemon: No such image: x' >&2
    exit 1
    ;;
  pull)
    echo pulled > "` + marker + `"
    ;;
esac
`
	//nolint:gosec // test helper script needs exec bit
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755))
	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}

	err := d.EnsureImage(context.Background(), &driver.RunOptions{Image: testImageRef})

	require.NoError(t, err)
	assert.FileExists(t, marker, "a not-found external image should trigger a pull")
	attemptsRaw, readErr := os.ReadFile(attempts) //nolint:gosec // test-controlled path
	require.NoError(t, readErr)
	assert.Equal(t, "1\n", string(attemptsRaw), "must inspect exactly once, no retries")
}

func TestEnsureImage_RealErrorDoesNotPull(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pulled")
	bin := fakeEnsureImageDocker(t, "Cannot connect to the Docker daemon", marker)
	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}

	err := d.EnsureImage(context.Background(), &driver.RunOptions{Image: testImageRef})

	require.Error(t, err)
	assert.NoFileExists(t, marker, "a genuine daemon failure must not trigger a pull")
}

func TestEnsureContainerRunning_AlreadyRunning(t *testing.T) {
	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: testDockerCmd}}
	container := &config.ContainerDetails{
		ID:    "c1",
		State: config.ContainerDetailsState{Status: config.ContainerStatusRunning},
	}

	require.NoError(t, d.ensureContainerRunning(context.Background(), container))
}

func TestEnsureContainerRunning_TerminalNotRestarted(t *testing.T) {
	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: testDockerCmd}}
	container := &config.ContainerDetails{
		ID:    "c1",
		State: config.ContainerDetailsState{Status: "dead"},
	}

	err := d.ensureContainerRunning(context.Background(), container)
	require.Error(t, err)
	assert.ErrorIs(t, err, docker.ErrContainerTerminal)
}

func TestEnsureContainerRunning_RecoversOnRetry(t *testing.T) {
	bin := fakeDocker(t, 2)
	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}

	require.NoError(t, d.ensureContainerRunning(context.Background(), newExitedContainer()))
}

func TestEnsureContainerRunning_NeverStartsFailsFast(t *testing.T) {
	bin := fakeDocker(t, -1)
	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}

	err := d.ensureContainerRunning(context.Background(), newExitedContainer())
	require.Error(t, err)
	assert.ErrorIs(t, err, docker.ErrContainerTerminal)
}

func TestCommitContainer_RunsDockerCommit(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "committed")
	script := `#!/bin/sh
case "$1" in
  ps)
    echo "c1"
    ;;
  inspect)
    echo '[{"ID":"c1","State":{"Status":"running"}}]'
    ;;
  commit)
    echo "$@" > "` + marker + `"
    ;;
esac
`
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker-fake")
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755)) //nolint:gosec

	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}
	fsTag := "ghcr.io/acme/snapshots:my-ws-20260731150405-fs"
	err := d.CommitContainer(context.Background(), "my-ws", fsTag)

	require.NoError(t, err)
	assert.FileExists(t, marker)
	contents, err := os.ReadFile(marker) //nolint:gosec // marker is t.TempDir()
	require.NoError(t, err)
	assert.Contains(t, string(contents), "commit")
	assert.Contains(t, string(contents), "c1")
	assert.Contains(t, string(contents), "ghcr.io/acme/snapshots:my-ws-20260731150405-fs")
	assert.Contains(t, string(contents), "LABEL sh.devsy.snapshot=true")
}

func TestCommitContainer_NotFound(t *testing.T) {
	script := `#!/bin/sh
case "$1" in
  ps)
    # Return empty - no containers found
    ;;
esac
`
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker-fake")
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755)) //nolint:gosec

	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}
	err := d.CommitContainer(context.Background(), "missing-ws", "tag:latest")
	require.Error(t, err)
}

func TestCommandDevContainer_CancelWrapsCtxErr(t *testing.T) {
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	script := `#!/bin/sh
case "$1" in
  inspect)
    echo '[{"ID":"c1","State":{"Status":"running"}}]'
    ;;
  exec)
    touch "` + ready + `"
    sleep 5
    ;;
esac
`
	bin := filepath.Join(dir, "docker-fake")
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755)) //nolint:gosec

	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin, ContainerID: "c1"}}

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

	err := d.CommandDevContainer(ctx, &driver.CommandParams{
		User:    rootUser,
		Command: "true",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestDeleteDevContainer_StopsRunningContainerBeforeRemove(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := `#!/bin/sh
echo "$1" >> "` + calls + `"
case "$1" in
  inspect)
    echo '[{"ID":"c1","State":{"Status":"running"}}]'
    ;;
  stop) ;;
  rm)
    if ! grep -q '^stop$' "` + calls + `"; then
      echo "cannot remove running container" >&2
      exit 1
    fi
    ;;
esac
`
	bin := filepath.Join(dir, "docker-fake")
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755)) //nolint:gosec

	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin, ContainerID: "c1"}}

	err := d.DeleteDevContainer(context.Background(), "ws1")
	require.NoError(t, err)

	logged, readErr := os.ReadFile(calls) //nolint:gosec // G304: test-controlled path
	require.NoError(t, readErr)
	assert.Contains(t, string(logged), "stop\n")
	assert.Contains(t, string(logged), "rm\n")
}

func TestEnsureContainerRunning_PausedUnpausesNotRestarts(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := `#!/bin/sh
echo "$1" >> "` + calls + `"
case "$1" in
  inspect)
    echo '[{"ID":"c1","State":{"Status":"running"}}]'
    ;;
  unpause)
    ;;
  start)
    echo "start should not be called on paused container" >&2
    exit 1
    ;;
esac
`
	bin := filepath.Join(dir, "docker-fake")
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755)) //nolint:gosec

	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}
	container := &config.ContainerDetails{
		ID:    "c1",
		State: config.ContainerDetailsState{Status: "paused"},
	}

	require.NoError(t, d.ensureContainerRunning(context.Background(), container))

	logged, readErr := os.ReadFile(calls) //nolint:gosec // test-controlled path
	require.NoError(t, readErr)
	assert.Contains(t, string(logged), "unpause\n")
	assert.NotContains(
		t,
		string(logged),
		"start\n",
		"paused containers should be unpaused, not started",
	)
}

func TestEnsureContainerRunning_RestartingWaitsNotStarts(t *testing.T) {
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	inspects := filepath.Join(dir, "inspects")
	script := `#!/bin/sh
echo "$1" >> "` + calls + `"
case "$1" in
  inspect)
    n=$(cat "` + inspects + `" 2>/dev/null || echo 0)
    n=$((n + 1))
    echo "$n" > "` + inspects + `"
    if [ "$n" -ge 2 ]; then
      echo '[{"ID":"c1","State":{"Status":"running"}}]'
    else
      echo '[{"ID":"c1","State":{"Status":"restarting"}}]'
    fi
    ;;
  start)
    echo "start should not be called on a restarting container" >&2
    exit 1
    ;;
esac
`
	bin := filepath.Join(dir, "docker-fake")
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755)) //nolint:gosec

	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}
	container := &config.ContainerDetails{
		ID:    "c1",
		State: config.ContainerDetailsState{Status: config.ContainerStatusRestarting},
	}

	require.NoError(t, d.ensureContainerRunning(context.Background(), container))

	logged, readErr := os.ReadFile(calls) //nolint:gosec // test-controlled path
	require.NoError(t, readErr)
	assert.NotContains(
		t,
		string(logged),
		"start\n",
		"restarting containers should be waited out, not started",
	)
}

func TestEnsureContainerRunning_UnknownStateIsTerminal(t *testing.T) {
	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: testDockerCmd}}
	container := &config.ContainerDetails{
		ID:    "c1",
		State: config.ContainerDetailsState{Status: "unknown"},
	}

	err := d.ensureContainerRunning(context.Background(), container)
	require.Error(t, err)
	assert.ErrorIs(t, err, docker.ErrContainerTerminal)
}

func TestRestartAndWait_ContextCanceledExitsEarly(t *testing.T) {
	dir := t.TempDir()
	startsFile := filepath.Join(dir, "starts")
	script := `#!/bin/sh
case "$1" in
  start)
    echo 1 >> "` + startsFile + `"
    echo started
    ;;
  inspect)
    echo '[{"ID":"c1","State":{"Status":"exited","ExitCode":1}}]'
    ;;
esac
`
	bin := filepath.Join(dir, "docker-fake")
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755)) //nolint:gosec

	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled immediately

	err := d.restartAndWait(
		ctx,
		newExitedContainer(),
		config.ContainerStatusExited,
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)

	// Ensure loop did not make start attempts
	_, readErr := os.ReadFile(startsFile) //nolint:gosec // test-controlled path
	assert.True(t, os.IsNotExist(readErr), "canceled context must abort before attempting restart")
}

func TestRestartAndWait_TerminalErrorExitsEarlyWithoutRetrying(t *testing.T) {
	dir := t.TempDir()
	startsFile := filepath.Join(dir, "starts")
	script := `#!/bin/sh
case "$1" in
  start)
    echo 1 >> "` + startsFile + `"
    echo started
    ;;
  inspect)
    echo '[{"ID":"c1","State":{"Status":"dead","ExitCode":1}}]'
    ;;
esac
`
	bin := filepath.Join(dir, "docker-fake")
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755)) //nolint:gosec

	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}
	err := d.restartAndWait(
		context.Background(),
		newExitedContainer(),
		config.ContainerStatusExited,
	)
	require.Error(t, err)
	assert.ErrorIs(t, err, docker.ErrContainerTerminal)

	// Should abort on attempt 1, so startsFile should have exactly 1 attempt
	startsData, readErr := os.ReadFile(startsFile) //nolint:gosec // test-controlled path
	require.NoError(t, readErr)
	lines := string(startsData)
	assert.Equal(
		t,
		"1\n",
		lines,
		"terminal state must abort immediately without continuing attempts",
	)
}

func TestIsTerminalOrContextError(t *testing.T) {
	assert.False(t, isTerminalOrContextError(nil))
	assert.False(t, isTerminalOrContextError(assert.AnError))
	assert.True(t, isTerminalOrContextError(docker.ErrContainerTerminal))
	assert.True(t, isTerminalOrContextError(context.Canceled))
	assert.True(t, isTerminalOrContextError(context.DeadlineExceeded))
	assert.True(t, isTerminalOrContextError(fmt.Errorf("wrapped: %w", docker.ErrContainerTerminal)))
	assert.True(t, isTerminalOrContextError(fmt.Errorf("wrapped: %w", context.Canceled)))
	assert.True(t, isTerminalOrContextError(fmt.Errorf("wrapped: %w", context.DeadlineExceeded)))
}

func TestStartContainerPreservesEngineError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	source := filepath.Join(t.TempDir(), "missing.sock")
	bin := filepath.Join(t.TempDir(), "engine")
	require.NoError(t, os.WriteFile(bin, []byte(`#!/bin/sh
if [ "$1" = run ]; then
 printf '%s\n' "bind source path does not exist: $2" "$ENGINE_TOKEN" >&2
 exit 17
fi
`), 0o600))
	// #nosec G302 -- only the test owner can execute this temporary fake engine.
	require.NoError(t, os.Chmod(bin, 0o700))
	d := &dockerDriver{
		Docker: &docker.DockerHelper{
			DockerCommand: bin,
			Environment:   []string{"ENGINE_TOKEN=private-engine-token"},
		},
	}
	var streamed engineOutputBuffer
	err := d.startContainer(
		context.Background(),
		t.TempDir(),
		[]string{testRunArg, source},
		&streamed,
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to start dev container")
	assert.Contains(t, err.Error(), "bind source path does not exist: "+source)
	assert.NotContains(t, err.Error(), "private-engine-token")
	assert.Contains(t, streamed.String(), "bind source path does not exist: "+source)
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr))
	assert.Equal(t, 17, exitErr.ExitCode())
}

func TestStartContainerBoundsEngineError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}
	bin := filepath.Join(t.TempDir(), "engine")
	require.NoError(t, os.WriteFile(bin, []byte(`#!/bin/sh
if [ "$1" = run ]; then
 i=0
 while [ "$i" -lt 2048 ]; do
 printf '%s' "`+strings.Repeat("x", 1024)+`" >&2
 i=$((i + 1))
 done
 printf '%s' tail-marker >&2
 exit 1
fi
`), 0o600))
	// #nosec G302 -- only the test owner can execute this temporary fake engine.
	require.NoError(t, os.Chmod(bin, 0o700))
	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin}}
	var streamed engineOutputBuffer
	err := d.startContainer(context.Background(), t.TempDir(), []string{testRunArg}, &streamed)
	require.Error(t, err)
	assert.Len(t, streamed.String(), 2048*1024+len("tail-marker"))
	assert.True(t, strings.HasSuffix(err.Error(), "tail-marker"))
	assert.LessOrEqual(t, len(err.Error()), subprocess.MaxCapturedBytes+100)
	assert.Contains(t, err.Error(), strings.Repeat("x", 100))
}

type engineOutputBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *engineOutputBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}

func (b *engineOutputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}
