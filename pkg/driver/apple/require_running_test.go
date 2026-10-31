package apple

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	statusExited = "exited"
	execUser     = "root"
)

func TestCommandDevContainerRequireRunningNeverStarts(t *testing.T) {
	for _, status := range []config.ContainerStatus{statusExited, "created", "paused", "stopped"} {
		t.Run(string(status), func(t *testing.T) {
			m := &mockClient{found: &config.ContainerDetails{
				ID: "c2", State: config.ContainerDetailsState{Status: status},
			}}
			d := &appleDriver{Apple: m}

			err := d.CommandDevContainer(context.Background(), &driver.CommandParams{
				WorkspaceID: "ws", User: execUser, Command: "true", RequireRunning: true,
			})

			require.ErrorIs(t, err, driver.ErrNotRunning)
			assert.Empty(t, m.calls)
			assert.Empty(t, m.ranArgs)
		})
	}
}

func TestCommandDevContainerRequireRunningExecsRunningContainer(t *testing.T) {
	m := &mockClient{found: &config.ContainerDetails{
		ID: "c2", State: config.ContainerDetailsState{Status: config.ContainerStatusRunning},
	}}
	d := &appleDriver{Apple: m}

	err := d.CommandDevContainer(context.Background(), &driver.CommandParams{
		WorkspaceID: "ws", User: execUser, Command: "true", RequireRunning: true,
	})

	require.NoError(t, err)
	assert.Equal(t, []string{appleExec}, m.calls)
}

func TestAppleDriverSupportsRequireRunning(t *testing.T) {
	assert.True(t, driver.DriverSupportsRequireRunning(&appleDriver{}))
}
