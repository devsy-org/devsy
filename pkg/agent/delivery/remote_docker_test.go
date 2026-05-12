package delivery

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRemoteDockerDelivery_Phase(t *testing.T) {
	d := &RemoteDockerDelivery{}
	assert.Equal(t, PhasePostStart, d.Phase())
}

func TestRemoteDockerDelivery_DeliverPreStart_ReturnsError(t *testing.T) {
	d := &RemoteDockerDelivery{}
	err := d.DeliverPreStart(context.Background(), PreStartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support pre-start")
}

func TestRemoteDockerDelivery_DeliverPostStart_RequiresContainerID(t *testing.T) {
	d := &RemoteDockerDelivery{}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{
		BinaryPath: "/bin/sh",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "container ID is required")
}

func TestRemoteDockerDelivery_Cleanup_IsNoOp(t *testing.T) {
	d := &RemoteDockerDelivery{}
	err := d.Cleanup(context.Background(), "workspace-123")
	assert.NoError(t, err)
}

func TestRemoteDockerDelivery_DockerCommand_Default(t *testing.T) {
	d := &RemoteDockerDelivery{}
	assert.Equal(t, "docker", d.dockerCommand())
}

func TestRemoteDockerDelivery_DockerCommand_Custom(t *testing.T) {
	d := &RemoteDockerDelivery{DockerCommand: "podman"}
	assert.Equal(t, "podman", d.dockerCommand())
}
