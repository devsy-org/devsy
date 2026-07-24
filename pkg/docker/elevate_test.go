package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	dockerCmd  = string(RuntimeDocker)
	dockerHost = "DOCKER_HOST=tcp://host:2375"
)

func TestElevatorFromName(t *testing.T) {
	tests := []struct {
		name       string
		wantNil    bool
		wantPrefix []string
		wantErr    bool
	}{
		{name: "", wantNil: true},
		{name: "none", wantNil: true},
		{name: "  None  ", wantNil: true},
		{name: elevationPkexec, wantPrefix: []string{elevationPkexec}},
		{name: "SUDO", wantPrefix: []string{elevationSudo}},
		{name: elevationDoas, wantPrefix: []string{elevationDoas}},
		{name: "gksu", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := ElevatorFromName(tt.name)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, e)
				return
			}
			require.NotNil(t, e)
			assert.Equal(t, tt.wantPrefix, e.prefix)
		})
	}
}

func TestElevatorWrap(t *testing.T) {
	e, err := ElevatorFromName(elevationPkexec)
	require.NoError(t, err)

	name, args := e.wrap(dockerCmd, nil, []string{"ps", "-q"})
	assert.Equal(t, elevationPkexec, name)
	assert.Equal(t, []string{dockerCmd, "ps", "-q"}, args)

	// No docker args.
	name, args = e.wrap("/usr/bin/docker", nil, nil)
	assert.Equal(t, elevationPkexec, name)
	assert.Equal(t, []string{"/usr/bin/docker"}, args)

	// Environment is forwarded through env(1).
	name, args = e.wrap(dockerCmd, []string{dockerHost}, []string{"ps"})
	assert.Equal(t, elevationPkexec, name)
	assert.Equal(t, []string{"env", dockerHost, dockerCmd, "ps"}, args)
}

func TestEnsureElevatedNoOpWithoutElevator(t *testing.T) {
	r := &DockerHelper{DockerCommand: dockerCmd}
	assert.NoError(t, r.EnsureElevated())
}

func TestBuildCmdWithoutElevator(t *testing.T) {
	r := &DockerHelper{DockerCommand: dockerCmd}
	cmd := r.buildCmd(t.Context(), "ps", "-q")
	assert.Equal(t, []string{dockerCmd, "ps", "-q"}, cmd.Args)
}

func TestBuildCmdWithElevator(t *testing.T) {
	e, err := ElevatorFromName(elevationSudo)
	require.NoError(t, err)
	// Mark authentication as already done so buildCmd does not attempt an
	// interactive prompt during the test.
	e.once.Do(func() {})

	r := &DockerHelper{DockerCommand: dockerCmd, Elevator: e}
	cmd := r.buildCmd(t.Context(), "ps", "-q")
	assert.Equal(t, []string{elevationSudo, dockerCmd, "ps", "-q"}, cmd.Args)
}

func TestBuildCmdWithElevatorForwardsEnv(t *testing.T) {
	e, err := ElevatorFromName(elevationSudo)
	require.NoError(t, err)
	e.once.Do(func() {})

	r := &DockerHelper{
		DockerCommand: dockerCmd,
		Environment:   []string{dockerHost},
		Elevator:      e,
	}
	cmd := r.buildCmd(t.Context(), "ps")
	assert.Equal(t,
		[]string{elevationSudo, "env", dockerHost, dockerCmd, "ps"},
		cmd.Args,
	)
}
