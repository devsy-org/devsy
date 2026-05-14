package docker

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRuntimeFromName(t *testing.T) {
	tests := []struct {
		input    string
		expected RuntimeName
	}{
		{"docker", RuntimeDocker},
		{"podman", RuntimePodman},
		{"nerdctl", RuntimeNerdctl},
		{"Docker", RuntimeDocker},
		{"Podman", RuntimePodman},
		{"NERDCTL", RuntimeNerdctl},
		{"", RuntimeDocker},
		{"unknown", RuntimeDocker},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			rt := RuntimeFromName(tt.input)
			assert.Equal(t, tt.expected, rt.Name())
		})
	}
}

func TestDockerRuntimeCapabilities(t *testing.T) {
	rt := RuntimeFromName("docker")
	assert.Equal(t, RuntimeDocker, rt.Name())
	assert.True(t, rt.SupportsInternalBuildKit())
	assert.True(t, rt.SupportsSignalProxy())
	assert.True(t, rt.SupportsMountConsistency())
	assert.False(t, rt.NeedsUserNamespaceArgs())
}

func TestPodmanRuntimeCapabilities(t *testing.T) {
	rt := RuntimeFromName("podman")
	assert.Equal(t, RuntimePodman, rt.Name())
	assert.False(t, rt.SupportsInternalBuildKit())
	assert.True(t, rt.SupportsSignalProxy())
	assert.True(t, rt.SupportsMountConsistency())
	assert.True(t, rt.NeedsUserNamespaceArgs())
}

func TestNerdctlRuntimeCapabilities(t *testing.T) {
	rt := RuntimeFromName("nerdctl")
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
		DockerCommand: "docker",
		Runtime:       podmanRuntime{},
	}
	rt := h.GetRuntime()
	assert.Equal(t, RuntimePodman, rt.Name(), "should use explicitly set runtime")
}

func TestDockerHelperIsPodmanDelegates(t *testing.T) {
	h := &DockerHelper{
		DockerCommand: "docker",
		Runtime:       podmanRuntime{},
	}
	assert.True(t, h.IsPodman())
	assert.False(t, h.IsNerdctl())
}

func TestDockerHelperIsNerdctlDelegates(t *testing.T) {
	h := &DockerHelper{
		DockerCommand: "docker",
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
