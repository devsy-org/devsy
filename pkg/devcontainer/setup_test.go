package devcontainer

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

const testLoginInteractiveShell = "loginInteractiveShell"

func TestBuildResult_DefaultUserEnvProbeOverride(t *testing.T) {
	r := &runner{
		WorkspaceConfig: &provider2.AgentWorkspaceInfo{
			CLIOptions: provider2.CLIOptions{
				DefaultUserEnvProbe: "none",
			},
		},
	}

	mergedConfig := &config.MergedDevContainerConfig{}
	mergedConfig.UserEnvProbe = testLoginInteractiveShell

	params := &setupContainerParams{
		rawConfig:           &config.DevContainerConfig{},
		mergedConfig:        mergedConfig,
		substitutionContext: &config.SubstitutionContext{},
		containerDetails:    &config.ContainerDetails{},
	}

	result := r.buildResult(params)
	if result.MergedConfig.UserEnvProbe != "none" {
		t.Errorf("expected UserEnvProbe=%q, got %q", "none", result.MergedConfig.UserEnvProbe)
	}
}

func TestBuildResult_DefaultUserEnvProbeEmpty(t *testing.T) {
	r := &runner{
		WorkspaceConfig: &provider2.AgentWorkspaceInfo{
			CLIOptions: provider2.CLIOptions{},
		},
	}

	mergedConfig := &config.MergedDevContainerConfig{}
	mergedConfig.UserEnvProbe = testLoginInteractiveShell

	params := &setupContainerParams{
		rawConfig:           &config.DevContainerConfig{},
		mergedConfig:        mergedConfig,
		substitutionContext: &config.SubstitutionContext{},
		containerDetails:    &config.ContainerDetails{},
	}

	result := r.buildResult(params)
	if result.MergedConfig.UserEnvProbe != testLoginInteractiveShell {
		t.Errorf(
			"expected UserEnvProbe=%q, got %q",
			testLoginInteractiveShell,
			result.MergedConfig.UserEnvProbe,
		)
	}
}
