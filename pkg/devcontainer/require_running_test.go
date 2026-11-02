package devcontainer

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// plainDriver does not advertise RequireRunning, like an older external runtime.
type plainDriver struct {
	driver.Driver
	commands int
}

func (d *plainDriver) CommandDevContainer(context.Context, *driver.CommandParams) error {
	d.commands++

	return nil
}

type capableDriver struct {
	plainDriver
	got *driver.CommandParams
}

func (d *capableDriver) CommandDevContainer(_ context.Context, p *driver.CommandParams) error {
	d.got = p

	return nil
}

func (d *capableDriver) SupportsRequireRunning() bool { return true }

const testCmd = "true"

func TestCommandRequireRunningWithoutCapabilityNeverExecs(t *testing.T) {
	d := &plainDriver{}
	r := &runner{driver: d}

	err := r.Command(context.Background(), CommandParams{Command: testCmd, RequireRunning: true})

	require.ErrorIs(t, err, ErrRequireRunningUnsupported)
	assert.Zero(t, d.commands, "the ordinary command path must not be used as a fallback")
}

func TestCommandDefaultsStillExecWithoutCapability(t *testing.T) {
	d := &plainDriver{}
	r := &runner{driver: d}

	require.NoError(t, r.Command(context.Background(), CommandParams{Command: testCmd}))
	assert.Equal(t, 1, d.commands)
}

func TestCommandPropagatesRequireRunning(t *testing.T) {
	d := &capableDriver{}
	r := &runner{driver: d, id: "ws"}

	err := r.Command(context.Background(), CommandParams{
		Command: testCmd, RawStdout: true, RequireRunning: true,
	})

	require.NoError(t, err)
	require.NotNil(t, d.got)
	assert.True(t, d.got.RequireRunning)
	assert.True(t, d.got.RawStdout)
	assert.Equal(t, "ws", d.got.WorkspaceID)
}
