package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireRunningDocker is a fake docker whose inspected state is fixed and whose
// calls are logged, so a test can prove nothing but inspect and exec ran.
func requireRunningDocker(t *testing.T, status, execBody string) (*dockerDriver, string) {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := `#!/bin/sh
echo "$1" >> "` + calls + `"
case "$1" in
  inspect)
    echo '[{"ID":"c1","State":{"Status":"` + status + `"}}]'
    ;;
  exec)
    ` + execBody + `
    ;;
esac
`
	bin := filepath.Join(dir, "docker-fake")
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755)) //nolint:gosec

	return &dockerDriver{Docker: &docker.DockerHelper{DockerCommand: bin, ContainerID: "c1"}}, calls
}

const shellTrue = "true"

func loggedCalls(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // test temp file
	require.NoError(t, err)

	return strings.Fields(string(data))
}

func TestCommandDevContainerRequireRunningNeverStarts(t *testing.T) {
	for _, status := range []string{"exited", "created", "paused", "restarting", "dead"} {
		t.Run(status, func(t *testing.T) {
			d, calls := requireRunningDocker(t, status, "")

			err := d.CommandDevContainer(context.Background(), &driver.CommandParams{
				User: rootUser, Command: shellTrue, RequireRunning: true,
			})

			require.ErrorIs(t, err, driver.ErrNotRunning)
			for _, call := range loggedCalls(t, calls) {
				assert.NotContains(t, []string{"start", "unpause", "restart", "exec"}, call)
			}
		})
	}
}

func TestCommandDevContainerRequireRunningExecsRunningContainer(t *testing.T) {
	d, calls := requireRunningDocker(t, "running", "echo ok")

	err := d.CommandDevContainer(context.Background(), &driver.CommandParams{
		User: rootUser, Command: shellTrue, RequireRunning: true,
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"inspect", "exec"}, loggedCalls(t, calls))
}

// A stop between inspect and exec surfaces as the exec error. It must not
// trigger a start or a second attempt.
func TestCommandDevContainerRequireRunningStopBetweenInspectAndExec(t *testing.T) {
	d, calls := requireRunningDocker(t, "running",
		`echo "container c1 is not running" >&2; exit 1`)

	err := d.CommandDevContainer(context.Background(), &driver.CommandParams{
		User: rootUser, Command: shellTrue, RequireRunning: true,
	})

	require.Error(t, err)
	got := loggedCalls(t, calls)
	assert.Equal(t, 1, countOf(got, "exec"))
	assert.NotContains(t, got, "start")
	assert.NotContains(t, got, "unpause")
}

func countOf(list []string, want string) int {
	n := 0
	for _, s := range list {
		if s == want {
			n++
		}
	}

	return n
}

func TestCommandDevContainerDefaultStillStartsStoppedContainer(t *testing.T) {
	d, calls := requireRunningDocker(t, "exited", "echo ok")

	_ = d.CommandDevContainer(context.Background(), &driver.CommandParams{
		User: rootUser, Command: shellTrue,
	})

	assert.Contains(t, loggedCalls(t, calls), "start")
}

func TestDockerDriverSupportsRequireRunning(t *testing.T) {
	assert.True(t, driver.DriverSupportsRequireRunning(&dockerDriver{}))
}
