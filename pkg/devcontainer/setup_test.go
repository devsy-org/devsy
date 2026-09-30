package devcontainer

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/agent/delivery"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDockerHostEnvKey = "DOCKER_HOST"

type setupSSHDriver struct {
	mockDriver
	command func(context.Context, *driver.CommandParams) error
}

func (d *setupSSHDriver) CommandDevContainer(
	ctx context.Context,
	params *driver.CommandParams,
) error {
	return d.command(ctx, params)
}

type writeCloser struct{ io.Writer }

func (writeCloser) Close() error { return nil }

func TestExecSetupSSHServer_SilentExecReturnsStartupSilence(t *testing.T) {
	stdin := bytes.NewBufferString("input")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	var got *driver.CommandParams
	d := &setupSSHDriver{command: func(ctx context.Context, params *driver.CommandParams) error {
		got = params
		<-ctx.Done()
		return ctx.Err()
	}}
	r := newTestRunner(d)

	err := r.execSetupSSHServer(
		context.Background(), agent.ExecRequest{
			Command: "ssh-server --stdio", Stdin: stdin, Stdout: stdout, Stderr: stderr,
		},
		agent.ExecStartupWatchdogOptions{Timeout: 50 * time.Millisecond},
	)
	var silenceErr *agent.ExecStartupSilenceError
	require.ErrorAs(t, err, &silenceErr)
	require.NotNil(t, got)
	assert.Equal(t, r.id, got.WorkspaceID)
	assert.Equal(t, "root", got.User)
	assert.Equal(t, "ssh-server --stdio", got.Command)
	assert.Same(t, stdin, got.Stdin)
	assert.NotNil(t, got.Stdout)
	assert.NotNil(t, got.Stderr)
	assert.True(t, got.RawStdout)
}

func TestExecSetupSSHServer_OutputDisarmsWatchdog(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			wrote := make(chan struct{})
			d := &setupSSHDriver{command: func(
				ctx context.Context,
				params *driver.CommandParams,
			) error {
				output := params.Stdout
				if stream == "stderr" {
					output = params.Stderr
				}
				_, _ = io.WriteString(output, "SSH activity")
				close(wrote)
				<-ctx.Done()
				return ctx.Err()
			}}
			r := newTestRunner(d)
			result := make(chan error, 1)
			go func() {
				result <- r.execSetupSSHServer(ctx, agent.ExecRequest{
					Command: "ssh-server --stdio", Stdout: io.Discard, Stderr: writeCloser{io.Discard},
				}, agent.ExecStartupWatchdogOptions{Timeout: 50 * time.Millisecond})
			}()
			<-wrote
			time.Sleep(100 * time.Millisecond)
			cancel()
			err := <-result
			assert.ErrorIs(t, err, context.Canceled)
			var silenceErr *agent.ExecStartupSilenceError
			assert.NotErrorAs(t, err, &silenceErr)
		})
	}
}

func TestNewAgentDelivery_RemoteDockerHostWiring(t *testing.T) {
	cases := []struct {
		name     string
		env      map[string]string
		wantType any
	}{
		{
			name:     "unset DOCKER_HOST uses local delivery",
			env:      nil,
			wantType: &delivery.LocalDockerDelivery{},
		},
		{
			name:     "unix socket DOCKER_HOST uses local delivery",
			env:      map[string]string{testDockerHostEnvKey: "unix:///var/run/docker.sock"},
			wantType: &delivery.LocalDockerDelivery{},
		},
		{
			name:     "ssh DOCKER_HOST uses remote delivery",
			env:      map[string]string{testDockerHostEnvKey: "ssh://user@localhost"},
			wantType: &delivery.RemoteDockerDelivery{},
		},
		{
			name:     "tcp DOCKER_HOST uses remote delivery",
			env:      map[string]string{testDockerHostEnvKey: "tcp://192.168.1.100:2376"},
			wantType: &delivery.RemoteDockerDelivery{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRunner(&mockDriver{})
			r.workspaceConfig.Agent.Driver = provider2.DockerDriver
			r.workspaceConfig.Agent.Docker = provider2.ProviderDockerDriverConfig{Env: tc.env}

			got := r.newAgentDelivery()
			if reflect.TypeOf(got) != reflect.TypeOf(tc.wantType) {
				t.Errorf("newAgentDelivery() = %T, want %T", got, tc.wantType)
			}
		})
	}
}

func TestShouldChownWorkspace(t *testing.T) {
	cases := []struct {
		name             string
		goos             string
		isDockerDriver   bool
		isPodman         bool
		driverNeedsChown bool
		want             bool
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
		{
			// microsandbox shares the workspace over virtiofs root-owned, so a
			// non-root remote user needs the chown even on macOS.
			name:             "driver needing chown chowns despite docker driver on macOS",
			goos:             goosDarwin,
			isDockerDriver:   true,
			isPodman:         false,
			driverNeedsChown: true,
			want:             true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shouldChownWorkspace(c.goos, c.isDockerDriver, c.isPodman, c.driverNeedsChown)
			if got != c.want {
				t.Errorf("shouldChownWorkspace(%q, %v, %v, %v) = %v, want %v",
					c.goos, c.isDockerDriver, c.isPodman, c.driverNeedsChown, got, c.want)
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
				workspaceConfig: &provider2.AgentWorkspaceInfo{
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
			opts: provider2.CLIOptions{PullFromInsideContainerOverride: new(true)},
			want: types.StrBool(stringTrue),
		},
		{
			name: "override false wins even with git source",
			opts: provider2.CLIOptions{PullFromInsideContainerOverride: new(false)},
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
		workspaceConfig: &provider2.AgentWorkspaceInfo{
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
		workspaceConfig: &provider2.AgentWorkspaceInfo{
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

const testAgentInstallPath = "/home/vscode/.local/bin/devsy"

func TestAgentContainerPath(t *testing.T) {
	cases := []struct {
		name             string
		driverName       string
		agentInstallPath string
		want             string
	}{
		{
			name:       "non-kubernetes driver always uses the default",
			driverName: provider2.DockerDriver,
			want:       pkgconfig.ContainerDevsyHelperLocation,
		},
		{
			name:       "kubernetes driver with no override uses the default",
			driverName: provider2.KubernetesDriver,
			want:       pkgconfig.ContainerDevsyHelperLocation,
		},
		{
			name:             "kubernetes driver with AGENT_INSTALL_PATH uses the override",
			driverName:       provider2.KubernetesDriver,
			agentInstallPath: testAgentInstallPath,
			want:             testAgentInstallPath,
		},
		{
			name:             "non-kubernetes driver ignores a stray AgentInstallPath value",
			driverName:       provider2.DockerDriver,
			agentInstallPath: testAgentInstallPath,
			want:             pkgconfig.ContainerDevsyHelperLocation,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &runner{
				workspaceConfig: &provider2.AgentWorkspaceInfo{
					Agent: provider2.ProviderAgentConfig{
						Driver: c.driverName,
						Kubernetes: provider2.ProviderKubernetesDriverConfig{
							AgentInstallPath: c.agentInstallPath,
						},
					},
				},
			}
			if got := r.agentContainerPath(); got != c.want {
				t.Errorf("agentContainerPath() = %q, want %q", got, c.want)
			}
		})
	}
}
