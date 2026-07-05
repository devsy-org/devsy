package docker

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDocker writes an executable shell script that emulates the docker CLI
// subcommands used by ensureContainerRunning. `inspect` reports "exited" until
// the number of prior `start` invocations reaches startsUntilRunning, after
// which it reports "running". A startsUntilRunning of -1 keeps it exited
// forever (a container that never boots).
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

func TestEnsureContainerRunning_AlreadyRunning(t *testing.T) {
	d := &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: testDockerCmd}}
	container := &config.ContainerDetails{
		ID:    "c1",
		State: config.ContainerDetailsState{Status: "running"},
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
