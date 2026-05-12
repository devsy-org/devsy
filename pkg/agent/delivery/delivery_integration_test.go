//go:build integration

package delivery

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testBinarySource(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("#!/bin/sh\necho hello\n")), nil
}

func dockerAvailable() bool {
	cmd := exec.Command("docker", "info")
	return cmd.Run() == nil
}

func TestLocalDockerDelivery_Integration(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker not available")
	}

	ctx := context.Background()
	d := &LocalDockerDelivery{DockerCommand: "docker"}
	workspaceID := "delivery-test-integration"

	runOpts := &driver.RunOptions{
		Mounts: []*config.Mount{},
		Env:    map[string]string{},
	}
	opts := PreStartOptions{
		WorkspaceID:  workspaceID,
		RunOptions:   runOpts,
		BinarySource: testBinarySource,
		Arch:         "amd64",
	}

	err := d.DeliverPreStart(ctx, opts)
	require.NoError(t, err)

	assert.Len(t, runOpts.Mounts, 1)
	assert.Equal(t, "devsy-agent-"+workspaceID, runOpts.Mounts[0].Source)
	assert.Equal(t, volumeMountPath, runOpts.Mounts[0].Target)
	assert.Equal(t, "volume", runOpts.Mounts[0].Type)

	// Verify volume exists
	out, err := exec.CommandContext(ctx, "docker", "volume", "inspect", "devsy-agent-"+workspaceID).
		CombinedOutput()
	require.NoError(t, err, "volume should exist: %s", string(out))

	// Verify binary is in the volume
	out, err = exec.CommandContext(ctx, "docker", "run", "--rm",
		"-v", "devsy-agent-"+workspaceID+":"+volumeMountPath,
		"busybox:latest", "ls", "-la", volumeMountPath+"/devsy",
	).CombinedOutput()
	require.NoError(t, err, "binary should exist in volume: %s", string(out))
	assert.Contains(t, string(out), "rwx")

	// Cleanup
	err = d.Cleanup(ctx, workspaceID)
	require.NoError(t, err)

	// Verify volume removed
	out, _ = exec.CommandContext(ctx, "docker", "volume", "inspect", "devsy-agent-"+workspaceID).
		CombinedOutput()
	assert.Contains(t, string(out), "No such volume")
}

func TestRemoteDockerDelivery_Integration(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker not available")
	}

	ctx := context.Background()
	containerID := "delivery-test-remote"

	// Create a test container
	out, err := exec.CommandContext(ctx, "docker", "run", "-d",
		"--name", containerID,
		"busybox:latest", "sleep", "60",
	).CombinedOutput()
	require.NoError(t, err, "failed to create test container: %s", string(out))
	defer func() {
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", containerID).Run()
	}()

	d := &RemoteDockerDelivery{
		DockerCommand: "docker",
		ContainerID:   containerID,
	}

	err = d.DeliverPostStart(ctx, PostStartOptions{
		WorkspaceID:  "test-workspace",
		BinarySource: testBinarySource,
		Arch:         "amd64",
	})
	require.NoError(t, err)

	// Verify binary exists in container
	out, err = exec.CommandContext(ctx, "docker", "exec", containerID,
		"ls", "-la", "/usr/local/bin/devsy",
	).CombinedOutput()
	require.NoError(t, err, "binary should exist in container: %s", string(out))
	assert.Contains(t, string(out), "rwx")
}
