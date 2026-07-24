package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		{name: "pkexec", wantPrefix: []string{"pkexec"}},
		{name: "SUDO", wantPrefix: []string{"sudo"}},
		{name: "doas", wantPrefix: []string{"doas"}},
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
	e, err := ElevatorFromName("pkexec")
	require.NoError(t, err)

	name, args := e.wrap("docker", []string{"ps", "-q"})
	assert.Equal(t, "pkexec", name)
	assert.Equal(t, []string{"docker", "ps", "-q"}, args)

	// No docker args.
	name, args = e.wrap("/usr/bin/docker", nil)
	assert.Equal(t, "pkexec", name)
	assert.Equal(t, []string{"/usr/bin/docker"}, args)
}

func TestEnsureElevatedNoOpWithoutElevator(t *testing.T) {
	r := &DockerHelper{DockerCommand: "docker"}
	assert.NoError(t, r.EnsureElevated())
}

func TestBuildCmdWithoutElevator(t *testing.T) {
	r := &DockerHelper{DockerCommand: "docker"}
	cmd := r.buildCmd(t.Context(), "ps", "-q")
	assert.Equal(t, []string{"docker", "ps", "-q"}, cmd.Args)
}

func TestBuildCmdWithElevator(t *testing.T) {
	e, err := ElevatorFromName("sudo")
	require.NoError(t, err)
	// Mark authentication as already done so buildCmd does not attempt an
	// interactive prompt during the test.
	e.once.Do(func() {})

	r := &DockerHelper{DockerCommand: "docker", Elevator: e}
	cmd := r.buildCmd(t.Context(), "ps", "-q")
	assert.Equal(t, []string{"sudo", "docker", "ps", "-q"}, cmd.Args)
}
