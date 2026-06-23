package devcontainer

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
)

func boolPtr(v bool) *bool { return &v }

func TestResolvePullFromInsideContainer(t *testing.T) {
	cases := []struct {
		name string
		opts provider2.CLIOptions
		repo string
		want types.StrBool
	}{
		{
			name: "override true wins",
			opts: provider2.CLIOptions{PullFromInsideContainerOverride: boolPtr(true)},
			want: types.StrBool(stringTrue),
		},
		{
			name: "override false wins even with git source",
			opts: provider2.CLIOptions{PullFromInsideContainerOverride: boolPtr(false)},
			repo: "https://github.com/example/repo",
			want: types.StrBool(stringFalse),
		},
		{
			name: "no override, no git source -> empty",
			opts: provider2.CLIOptions{},
			want: "",
		},
		{
			name: "no override, no crane template -> empty even with git source",
			opts: provider2.CLIOptions{},
			repo: "https://github.com/example/repo",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resolvePullFromInsideContainer(c.opts, c.repo)
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

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
