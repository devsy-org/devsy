package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeFromName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected RuntimeName
	}{
		{"docker", string(RuntimeDocker), RuntimeDocker},
		{"podman", string(RuntimePodman), RuntimePodman},
		{"nerdctl", string(RuntimeNerdctl), RuntimeNerdctl},
		{"Docker-uppercase", "Docker", RuntimeDocker},
		{"Podman-uppercase", "Podman", RuntimePodman},
		{"NERDCTL-uppercase", "NERDCTL", RuntimeNerdctl},
		{"empty", "", RuntimeDocker},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, err := RuntimeFromName(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, rt.Name())
		})
	}
}

func TestRuntimeFromNameUnknown(t *testing.T) {
	_, err := RuntimeFromName("unknown")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown container runtime")
}

func TestDockerRuntimeCapabilities(t *testing.T) {
	rt, err := RuntimeFromName(string(RuntimeDocker))
	require.NoError(t, err)
	assert.Equal(t, RuntimeDocker, rt.Name())
	assert.True(t, rt.SupportsInternalBuildKit())
	assert.True(t, rt.SupportsSignalProxy())
	assert.True(t, rt.SupportsMountConsistency())
	assert.False(t, rt.NeedsUserNamespaceArgs())
}

func TestPodmanRuntimeCapabilities(t *testing.T) {
	rt, err := RuntimeFromName(string(RuntimePodman))
	require.NoError(t, err)
	assert.Equal(t, RuntimePodman, rt.Name())
	assert.False(t, rt.SupportsInternalBuildKit())
	assert.True(t, rt.SupportsSignalProxy())
	assert.True(t, rt.SupportsMountConsistency())
	assert.True(t, rt.NeedsUserNamespaceArgs())
}

func TestNerdctlRuntimeCapabilities(t *testing.T) {
	rt, err := RuntimeFromName(string(RuntimeNerdctl))
	require.NoError(t, err)
	assert.Equal(t, RuntimeNerdctl, rt.Name())
	assert.True(t, rt.SupportsInternalBuildKit())
	assert.False(t, rt.SupportsSignalProxy())
	assert.False(t, rt.SupportsMountConsistency())
	assert.False(t, rt.NeedsUserNamespaceArgs())
}

func TestDockerHelperGetRuntimeFallback(t *testing.T) {
	h := &DockerHelper{DockerCommand: "nonexistent-binary-xyz"}
	rt := h.GetRuntime()
	assert.Equal(t, RuntimeDocker, rt.Name(), "should fall back to docker for unknown binary")
}

func TestDockerHelperGetRuntimeExplicit(t *testing.T) {
	h := &DockerHelper{
		DockerCommand: string(RuntimeDocker),
		Runtime:       podmanRuntime{},
	}
	rt := h.GetRuntime()
	assert.Equal(t, RuntimePodman, rt.Name(), "should use explicitly set runtime")
}

func TestDockerHelperIsPodmanDelegates(t *testing.T) {
	h := &DockerHelper{
		DockerCommand: string(RuntimeDocker),
		Runtime:       podmanRuntime{},
	}
	assert.True(t, h.IsPodman())
	assert.False(t, h.IsNerdctl())
}

func TestDockerHelperIsNerdctlDelegates(t *testing.T) {
	h := &DockerHelper{
		DockerCommand: string(RuntimeDocker),
		Runtime:       nerdctlRuntime{},
	}
	assert.False(t, h.IsPodman())
	assert.True(t, h.IsNerdctl())
}

func TestDetectRuntimeCaching(t *testing.T) {
	rt1 := DetectRuntime("nonexistent-binary-abc")
	rt2 := DetectRuntime("nonexistent-binary-abc")
	assert.Equal(t, rt1, rt2, "same binary should return same cached runtime")
}
