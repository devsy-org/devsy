package agentworkspace

import (
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/provider"
)

func TestWaitForDockerPropagatesResolvedPath(t *testing.T) {
	const resolvedPath = "/Users/dev/.rd/bin/docker"

	for _, configuredPath := range []string{"", "docker"} {
		t.Run(configuredPath, func(t *testing.T) {
			workspaceInfo := &provider.AgentWorkspaceInfo{}
			workspaceInfo.Agent.Docker.Path = configuredPath
			initializer := &workspaceInitializer{workspaceInfo: workspaceInfo}
			resultChan := make(chan dockerInstallResult, 1)
			resultChan <- dockerInstallResult{path: resolvedPath}

			if err := initializer.waitForDocker(resultChan); err != nil {
				t.Fatalf("waitForDocker() error = %v", err)
			}
			if got := workspaceInfo.Agent.Docker.Path; got != resolvedPath {
				t.Fatalf("Docker.Path = %q, want %q", got, resolvedPath)
			}
		})
	}
}

func TestWaitForDockerPreservesExplicitPath(t *testing.T) {
	const customPath = "/opt/custom/bin/docker"
	workspaceInfo := &provider.AgentWorkspaceInfo{}
	workspaceInfo.Agent.Docker.Path = customPath
	initializer := &workspaceInitializer{workspaceInfo: workspaceInfo}
	resultChan := make(chan dockerInstallResult, 1)
	resultChan <- dockerInstallResult{path: "/Users/dev/.rd/bin/docker"}

	if err := initializer.waitForDocker(resultChan); err != nil {
		t.Fatalf("waitForDocker() error = %v", err)
	}
	if got := workspaceInfo.Agent.Docker.Path; got != customPath {
		t.Fatalf("Docker.Path = %q, want explicit path %q", got, customPath)
	}
}

func TestWaitForDockerReturnsDiscoveryError(t *testing.T) {
	discoveryErr := errors.New("docker not found")
	initializer := &workspaceInitializer{workspaceInfo: &provider.AgentWorkspaceInfo{}}
	resultChan := make(chan dockerInstallResult, 1)
	resultChan <- dockerInstallResult{err: discoveryErr}

	if err := initializer.waitForDocker(resultChan); !errors.Is(err, discoveryErr) {
		t.Fatalf("waitForDocker() error = %v, want wrapped %v", err, discoveryErr)
	}
}
