package docker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
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
// Empty string defaults to Docker. Unknown non-empty names return an error
// to catch typos in provider configuration.
func RuntimeFromName(name string) (ContainerRuntime, error) {
	switch RuntimeName(strings.ToLower(strings.TrimSpace(name))) {
	case "", RuntimeDocker:
		return dockerRuntime{}, nil
	case RuntimePodman:
		return podmanRuntime{}, nil
	case RuntimeNerdctl:
		return nerdctlRuntime{}, nil
	default:
		return nil, fmt.Errorf("unknown container runtime %q", name)
	}
}

const detectTimeout = 5 * time.Second

var runtimeCache = &runtimeDetectionCache{entries: make(map[string]ContainerRuntime)}

type runtimeDetectionCache struct {
	mu      sync.Mutex
	entries map[string]ContainerRuntime
}

func (c *runtimeDetectionCache) get(dockerCommand string) ContainerRuntime {
	c.mu.Lock()
	if rt, ok := c.entries[dockerCommand]; ok {
		c.mu.Unlock()
		return rt
	}
	c.mu.Unlock()

	rt := detect(dockerCommand)

	c.mu.Lock()
	c.entries[dockerCommand] = rt
	c.mu.Unlock()
	return rt
}

func detect(dockerCommand string) ContainerRuntime {
	ctx, cancel := context.WithTimeout(context.Background(), detectTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, dockerCommand, "--version").Output()
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
