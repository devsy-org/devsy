package docker

import (
	"context"
	"os/exec"
	"strings"
	"sync"
)

// RuntimeName identifies a container runtime.
type RuntimeName string

const (
	RuntimeDocker  RuntimeName = "docker"
	RuntimePodman  RuntimeName = "podman"
	RuntimeNerdctl RuntimeName = "nerdctl"
)

// ContainerRuntime describes the capabilities of a container runtime (Docker, Podman, nerdctl).
// Rather than checking IsPodman()/IsNerdctl() at every call site, callers query capability
// methods that express *why* the code branches.
type ContainerRuntime interface {
	Name() RuntimeName
	SupportsInternalBuildKit() bool
	SupportsSignalProxy() bool
	SupportsMountConsistency() bool
	NeedsUserNamespaceArgs() bool
	GPUAvailable(ctx context.Context, helper *DockerHelper) (bool, error)
}

type dockerRuntime struct{}

func (dockerRuntime) Name() RuntimeName              { return RuntimeDocker }
func (dockerRuntime) SupportsInternalBuildKit() bool { return true }
func (dockerRuntime) SupportsSignalProxy() bool      { return true }
func (dockerRuntime) SupportsMountConsistency() bool { return true }
func (dockerRuntime) NeedsUserNamespaceArgs() bool   { return false }

func (dockerRuntime) GPUAvailable(ctx context.Context, h *DockerHelper) (bool, error) {
	out, err := h.buildCmd(ctx, "info", "-f", "{{.Runtimes.nvidia}}").Output()
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(out), "nvidia-container-runtime"), nil
}

type podmanRuntime struct{}

func (podmanRuntime) Name() RuntimeName              { return RuntimePodman }
func (podmanRuntime) SupportsInternalBuildKit() bool { return false }
func (podmanRuntime) SupportsSignalProxy() bool      { return true }
func (podmanRuntime) SupportsMountConsistency() bool { return true }
func (podmanRuntime) NeedsUserNamespaceArgs() bool   { return true }

func (podmanRuntime) GPUAvailable(ctx context.Context, h *DockerHelper) (bool, error) {
	out, err := h.buildCmd(ctx, "info", "-f", "{{.Host.CDIDevices}}").Output()
	if err != nil {
		return false, nil
	}
	return strings.Contains(strings.ToLower(string(out)), "nvidia"), nil
}

type nerdctlRuntime struct{}

func (nerdctlRuntime) Name() RuntimeName              { return RuntimeNerdctl }
func (nerdctlRuntime) SupportsInternalBuildKit() bool { return true }
func (nerdctlRuntime) SupportsSignalProxy() bool      { return false }
func (nerdctlRuntime) SupportsMountConsistency() bool { return false }
func (nerdctlRuntime) NeedsUserNamespaceArgs() bool   { return false }

func (nerdctlRuntime) GPUAvailable(ctx context.Context, h *DockerHelper) (bool, error) {
	out, err := h.buildCmd(ctx, "info", "-f", "{{.Runtimes.nvidia}}").Output()
	if err != nil {
		return false, nil
	}
	return strings.Contains(string(out), "nvidia-container-runtime"), nil
}

// DetectRuntime probes the binary at dockerCommand to determine which runtime it is.
// The result is cached per binary path.
func DetectRuntime(dockerCommand string) ContainerRuntime {
	return runtimeCache.get(dockerCommand)
}

// RuntimeFromName returns a ContainerRuntime for the given explicit name.
// Falls back to Docker for unrecognized values.
func RuntimeFromName(name string) ContainerRuntime {
	switch RuntimeName(strings.ToLower(name)) {
	case RuntimePodman:
		return podmanRuntime{}
	case RuntimeNerdctl:
		return nerdctlRuntime{}
	default:
		return dockerRuntime{}
	}
}

var runtimeCache = &runtimeDetectionCache{entries: make(map[string]ContainerRuntime)}

type runtimeDetectionCache struct {
	mu      sync.Mutex
	entries map[string]ContainerRuntime
}

func (c *runtimeDetectionCache) get(dockerCommand string) ContainerRuntime {
	c.mu.Lock()
	defer c.mu.Unlock()

	if rt, ok := c.entries[dockerCommand]; ok {
		return rt
	}

	rt := detect(dockerCommand)
	c.entries[dockerCommand] = rt
	return rt
}

func detect(dockerCommand string) ContainerRuntime {
	out, err := exec.Command(dockerCommand, "--version").Output()
	if err != nil {
		return dockerRuntime{}
	}
	lower := strings.ToLower(string(out))
	if strings.Contains(lower, "podman") {
		return podmanRuntime{}
	}
	if strings.Contains(lower, "nerdctl") {
		return nerdctlRuntime{}
	}
	return dockerRuntime{}
}
