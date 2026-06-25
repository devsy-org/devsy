package devcontainer

import (
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
)

func boolPtr(v bool) *bool { return &v }

func TestShouldChownWorkspace(t *testing.T) {
	cases := []struct {
		name           string
		goos           string
		isDockerDriver bool
		isPodman       bool
		want           bool
	}{
		{
			name: "linux docker host always chowns",
			goos: goosLinux, isDockerDriver: true, isPodman: false, want: true,
		},
		{
			name: "non-docker driver always chowns (stream mounts)",
			goos: goosDarwin, isDockerDriver: false, isPodman: false, want: true,
		},
		{
			name: "docker desktop on macOS skips chown",
			goos: goosDarwin, isDockerDriver: true, isPodman: false, want: false,
		},
		{
			name: "docker desktop on windows skips chown",
			goos: goosWindows, isDockerDriver: true, isPodman: false, want: false,
		},
		{
			// Regression: previously skipped because podman uses the docker
			// driver, breaking non-root remoteUser on macOS/Windows.
			name: "podman on macOS chowns despite docker driver",
			goos: goosDarwin, isDockerDriver: true, isPodman: true, want: true,
		},
		{
			name: "podman on windows chowns despite docker driver",
			goos: goosWindows, isDockerDriver: true, isPodman: true, want: true,
		},
		{
			name: "podman on linux chowns",
			goos: goosLinux, isDockerDriver: true, isPodman: true, want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shouldChownWorkspace(c.goos, c.isDockerDriver, c.isPodman)
			if got != c.want {
				t.Errorf("shouldChownWorkspace(%q, %v, %v) = %v, want %v",
					c.goos, c.isDockerDriver, c.isPodman, got, c.want)
			}
		})
	}
}

func TestRunnerIsPodmanRuntime(t *testing.T) {
	cases := []struct {
		name    string
		runtime string
		want    bool
	}{
		{name: "podman", runtime: string(docker.RuntimePodman), want: true},
		{name: "podman mixed case", runtime: "Podman", want: true},
		{name: "docker runtime", runtime: string(docker.RuntimeDocker), want: false},
		{name: "empty defaults to non-podman", runtime: "", want: false},
		{name: "nerdctl", runtime: string(docker.RuntimeNerdctl), want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &runner{
				WorkspaceConfig: &provider2.AgentWorkspaceInfo{
					Agent: provider2.ProviderAgentConfig{
						Docker: provider2.ProviderDockerDriverConfig{Runtime: c.runtime},
					},
				},
			}
			if got := r.isPodmanRuntime(); got != c.want {
				t.Errorf("isPodmanRuntime() with runtime=%q = %v, want %v", c.runtime, got, c.want)
			}
		})
	}
}

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
