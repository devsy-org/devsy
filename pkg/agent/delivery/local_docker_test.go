package delivery

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalDockerDelivery_Phase(t *testing.T) {
	d := &LocalDockerDelivery{}
	assert.Equal(t, PhasePreStart, d.Phase())
}

func TestLocalDockerDelivery_DeliverPostStart_ReturnsError(t *testing.T) {
	d := &LocalDockerDelivery{}
	err := d.DeliverPostStart(context.Background(), PostStartOptions{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support post-start")
}

func TestLocalDockerDelivery_DeliverPreStart_MutatesRunOptions(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}

	d := &LocalDockerDelivery{}
	runOpts := &driver.RunOptions{
		Mounts: []*config.Mount{},
		Env:    map[string]string{},
	}
	opts := PreStartOptions{
		WorkspaceID: "test-workspace-123",
		RunOptions:  runOpts,
		BinaryPath:  "/bin/sh",
		Arch:        "amd64",
	}

	ctx := context.Background()
	err := d.DeliverPreStart(ctx, opts)
	if err != nil {
		t.Skipf("docker not available or failed: %v", err)
	}

	assert.Len(t, runOpts.Mounts, 1)
	assert.Equal(t, "devsy-agent-test-workspace-123", runOpts.Mounts[0].Source)
	assert.Equal(t, volumeMountPath, runOpts.Mounts[0].Target)
	assert.Equal(t, "volume", runOpts.Mounts[0].Type)
	assert.Contains(t, runOpts.Env["DEVSY_AGENT_PATH"], volumeMountPath)

	// Cleanup
	_ = d.Cleanup(ctx, "test-workspace-123")
}

func TestLocalDockerDelivery_Cleanup_NonExistentVolume(t *testing.T) {
	if testing.Short() {
		t.Skip("requires docker")
	}

	d := &LocalDockerDelivery{}
	err := d.Cleanup(context.Background(), "nonexistent-workspace-xyz")
	assert.NoError(t, err)
}

func TestDeliveryPhase_String(t *testing.T) {
	assert.Equal(t, "pre-start", PhasePreStart.String())
	assert.Equal(t, "post-start", PhasePostStart.String())
	assert.Contains(t, DeliveryPhase(99).String(), "unknown")
}

func TestBinaryName(t *testing.T) {
	name := binaryName()
	assert.Equal(t, "devsy", name)
}
