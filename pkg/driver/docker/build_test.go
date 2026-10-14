package docker

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/build"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func writeHelperScript(t *testing.T, dir, name, output string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	content := "#!/bin/sh\necho '" + output + "'\n"
	//nolint:gosec // test helper script needs exec bit
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
	return path
}

func TestSelectStrategy_DockerRuntime_AllowsBuildKit(t *testing.T) {
	tmp := t.TempDir()
	bin := writeHelperScript(t, tmp, "docker-fake", "Docker version 24.0.7, build afdd53b")

	helper := &docker.DockerHelper{
		DockerCommand: bin,
		Builder:       docker.DockerBuilderBuildKit,
	}
	d := &dockerDriver{Docker: helper}
	o := &buildOrchestrator{driver: d}

	strategy := o.selectStrategy(provider.BuildOptions{})

	assert.IsType(t, &buildkitStrategy{}, strategy)
	assert.Equal(t, "internal buildkit", strategy.name())
}

func TestSelectStrategy_PodmanRuntime_ForcesCLIBuild(t *testing.T) {
	tmp := t.TempDir()
	bin := writeHelperScript(t, tmp, "podman-fake", "podman version 4.9.3")

	helper := &docker.DockerHelper{
		DockerCommand: bin,
		Builder:       docker.DockerBuilderBuildKit,
	}
	d := &dockerDriver{Docker: helper}
	o := &buildOrchestrator{driver: d}

	strategy := o.selectStrategy(provider.BuildOptions{
		CLIOptions: provider.CLIOptions{ForceInternalBuildKit: true},
	})

	assert.IsType(t, &dockerBuildxStrategy{}, strategy)
	assert.Equal(t, "docker buildx build", strategy.name())
}

func TestSelectStrategy_PodmanRuntime_IgnoresBuilderConfig(t *testing.T) {
	tmp := t.TempDir()
	bin := writeHelperScript(t, tmp, "podman-fake", "podman version 4.9.3")

	builders := []docker.DockerBuilder{
		docker.DockerBuilderDefault,
		docker.DockerBuilderBuildX,
		docker.DockerBuilderBuildKit,
	}

	for _, builder := range builders {
		t.Run(builder.String(), func(t *testing.T) {
			helper := &docker.DockerHelper{
				DockerCommand: bin,
				Builder:       builder,
			}
			d := &dockerDriver{Docker: helper}
			o := &buildOrchestrator{driver: d}

			strategy := o.selectStrategy(provider.BuildOptions{})

			assert.IsType(t, &dockerBuildxStrategy{}, strategy,
				"Podman should always use CLI build regardless of builder config %q", builder)
		})
	}
}

func TestSelectStrategy_DockerRuntime_BuildxWhenAvailable(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}

	helper := &docker.DockerHelper{
		DockerCommand: "docker",
		Builder:       docker.DockerBuilderDefault,
	}
	d := &dockerDriver{Docker: helper}
	o := &buildOrchestrator{driver: d}

	strategy := o.selectStrategy(provider.BuildOptions{})

	// With real Docker, buildx is typically available, so expect buildx strategy.
	// If buildx isn't installed, it falls back to buildkit - both are valid for Docker.
	switch strategy.(type) {
	case *dockerBuildxStrategy:
		// Docker with buildx available
	case *buildkitStrategy:
		// Docker without buildx - still valid, not Podman
	default:
		t.Fatalf("unexpected strategy type: %T", strategy)
	}
}

func TestBuildDockerBuildxArgs_PullAndNoCache(t *testing.T) {
	tests := []struct {
		name       string
		opts       *build.BuildOptions
		wantPull   bool
		wantNoCach bool
	}{
		{name: "neither", opts: &build.BuildOptions{}},
		{name: "pull only", opts: &build.BuildOptions{Pull: true}, wantPull: true},
		{name: "no-cache only", opts: &build.BuildOptions{NoCache: true}, wantNoCach: true},
		{
			name:       "both",
			opts:       &build.BuildOptions{Pull: true, NoCache: true},
			wantPull:   true,
			wantNoCach: true,
		},
	}
	dockerRt, err := docker.RuntimeFromName(string(docker.RuntimeDocker))
	require.NoError(t, err)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := buildDockerBuildxArgs(tt.opts, "", dockerRt)
			assert.Equal(t, tt.wantPull, slices.Contains(args, "--pull"), "args=%v", args)
			assert.Equal(t, tt.wantNoCach, slices.Contains(args, "--no-cache"), "args=%v", args)
		})
	}
}

const devcontainerMetadataLabel = "devcontainer.metadata"

func TestBuildDockerBuildxArgs_PodmanEscapesDollarInLabels(t *testing.T) {
	podmanRt, err := docker.RuntimeFromName(string(docker.RuntimePodman))
	require.NoError(t, err)

	opts := &build.BuildOptions{
		Labels: map[string]string{
			devcontainerMetadataLabel: `[{"mounts":[{"source":"${localEnv:HOME}/.cache","target":"/cache","type":"bind"}]}]`,
		},
	}

	args := buildDockerBuildxArgs(
		opts,
		"",
		podmanRt,
	)
	expected := devcontainerMetadataLabel +
		`=[{"mounts":[{"source":"${_:-$}{localEnv:HOME}/.cache","target":"/cache","type":"bind"}]}]`
	var found string
	for i := range len(args) - 1 {
		if args[i] == "--label" {
			found = args[i+1]
			break
		}
	}
	assert.Equal(t, expected, found)
}

func TestBuildDockerBuildxArgs_DockerPreservesDollarInLabels(t *testing.T) {
	dockerRt, err := docker.RuntimeFromName(string(docker.RuntimeDocker))
	require.NoError(t, err)

	opts := &build.BuildOptions{
		Labels: map[string]string{
			devcontainerMetadataLabel: `[{"mounts":[{"source":"${localEnv:HOME}/.cache","target":"/cache","type":"bind"}]}]`,
		},
	}

	args := buildDockerBuildxArgs(
		opts,
		"",
		dockerRt,
	)
	expected := devcontainerMetadataLabel +
		`=[{"mounts":[{"source":"${localEnv:HOME}/.cache","target":"/cache","type":"bind"}]}]`
	var found string
	for i := range len(args) - 1 {
		if args[i] == "--label" {
			found = args[i+1]
			break
		}
	}
	assert.Equal(t, expected, found)
}

func TestBuildDockerBuildxArgs_PodmanDoesNotMutateLabels(t *testing.T) {
	podmanRt, err := docker.RuntimeFromName(string(docker.RuntimePodman))
	require.NoError(t, err)

	const raw = `[{"mounts":[{"source":"${localEnv:HOME}/.cache","target":"/cache","type":"bind"}]}]`
	opts := &build.BuildOptions{
		Labels: map[string]string{
			devcontainerMetadataLabel: raw,
		},
	}

	_ = buildDockerBuildxArgs(
		opts,
		"",
		podmanRt,
	)
	assert.Equal(t, raw, opts.Labels[devcontainerMetadataLabel])
}

func TestBuildxSecretArgs(t *testing.T) {
	env, args, err := buildxSecretArgs([]string{"NPM_TOKEN=abc123", "CONN=user=admin"})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"DEVSY_BUILD_SECRET_NPM_TOKEN=abc123",
		"DEVSY_BUILD_SECRET_CONN=user=admin",
	}, env)
	assert.Equal(t, []string{
		"--secret", "id=NPM_TOKEN,env=DEVSY_BUILD_SECRET_NPM_TOKEN",
		"--secret", "id=CONN,env=DEVSY_BUILD_SECRET_CONN",
	}, args)
}

func TestBuildxSecretArgs_Empty(t *testing.T) {
	env, args, err := buildxSecretArgs(nil)
	require.NoError(t, err)
	assert.Empty(t, env)
	assert.Empty(t, args)
}

func TestBuildxSecretArgs_Invalid(t *testing.T) {
	_, _, err := buildxSecretArgs([]string{"NOEQUALS"})
	require.Error(t, err)
}

func TestTailBuffer(t *testing.T) {
	// Write always reports full consumption so it never short-writes a MultiWriter.
	b := &tailBuffer{limit: 4}
	n, err := b.Write([]byte("ab"))
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Equal(t, "ab", b.String())

	// Straddling the limit keeps only the last `limit` bytes.
	_, _ = b.Write([]byte("cde"))
	assert.Equal(t, 4, b.Len())
	assert.Equal(t, "bcde", b.String())

	// A single oversized write keeps its tail (buildx emits the real error last).

	b2 := &tailBuffer{limit: 4}
	n, err = b2.Write([]byte("0123456789"))
	require.NoError(t, err)
	assert.Equal(t, 10, n)
	assert.Equal(t, "6789", b2.String())
}

type testBuildStrategy struct {
	output string
}

func (s testBuildStrategy) build(
	_ context.Context,
	writer io.Writer,
	_ string,
	_ *build.BuildOptions,
) error {
	_, err := io.WriteString(writer, s.output)
	return err
}

func (testBuildStrategy) name() string { return "test build" }

func TestExecuteBuildDecodesStructuredOutput(t *testing.T) {
	logs := log.InitTestObserved(t, zapcore.DebugLevel)
	driverUnderTest := &dockerDriver{}
	strategy := testBuildStrategy{
		output: "build starting\n" +
			`{"level":"debug","msg":"credential request received"}` + "\n" +
			"build complete\n",
	}

	err := driverUnderTest.executeBuild(
		t.Context(),
		strategy,
		driver.BuildRequest{},
		&build.BuildOptions{},
	)
	require.NoError(t, err)

	entries := logs.All()
	require.Len(t, entries, 4)
	assert.Equal(t, "build with test build", entries[0].Message)
	assert.Equal(t, "build starting", entries[1].Message)
	assert.Equal(t, zapcore.DebugLevel, entries[2].Level)
	assert.Equal(t, "credential request received", entries[2].Message)
	assert.Equal(t, "build complete", entries[3].Message)
	assert.NotContains(t, entries[2].Message, `{"level":"debug"`)
}

func elevatedDockerStrategy(t *testing.T, env ...string) *buildkitStrategy {
	t.Helper()
	bin := writeHelperScript(t, t.TempDir(), "docker-fake", "Docker version 24.0.7, build afdd53b")
	elevator, err := docker.ElevatorFromName("sudo")
	require.NoError(t, err)
	return &buildkitStrategy{driver: &dockerDriver{Docker: &docker.DockerHelper{
		DockerCommand: bin,
		Builder:       docker.DockerBuilderBuildKit,
		Elevator:      elevator,
		Environment:   env,
	}}}
}

func TestBuildkitNewDockerClient_ElevatedRejectsDaemonOverrides(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_CONFIG", "/process/config")

	s := elevatedDockerStrategy(t,
		"DOCKER_HOST=ssh://user@remote",
		"DOCKER_CONFIG=/provider/config",
		"DOCKER_CONTEXT=remote",
	)
	client, err := s.newDockerClient(context.Background())

	require.Error(t, err)
	assert.Nil(t, client)
	assert.ErrorContains(t, err, "DOCKER_HOST, DOCKER_CONTEXT, DOCKER_CONFIG")
	assert.NotContains(t, err.Error(), "ssh://user@remote", "values must not be printed")
}

func TestBuildkitNewDockerClient_ElevatedKeepsProcessClientWithoutOverrides(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")

	for name, env := range map[string][]string{
		"none":               nil,
		"equal host":         {"DOCKER_HOST=unix:///var/run/docker.sock"},
		"unrelated variable": {"FOO=bar"},
	} {
		t.Run(name, func(t *testing.T) {
			client, err := elevatedDockerStrategy(t, env...).newDockerClient(context.Background())
			require.NoError(t, err)
			require.NotNil(t, client)
			t.Cleanup(func() { _ = client.Close() })
		})
	}
}

func TestBuildkitNewDockerClient_ForceInternalBuildKitNeverSwitchesToBuildx(t *testing.T) {
	bin := writeHelperScript(t, t.TempDir(), "docker-fake", "Docker version 24.0.7, build afdd53b")
	elevator, err := docker.ElevatorFromName("sudo")
	require.NoError(t, err)
	o := &buildOrchestrator{driver: &dockerDriver{Docker: &docker.DockerHelper{
		DockerCommand: bin,
		Builder:       docker.DockerBuilderBuildX,
		Elevator:      elevator,
		Environment:   []string{"DOCKER_HOST=ssh://user@remote"},
	}}}

	strategy := o.selectStrategy(provider.BuildOptions{
		CLIOptions: provider.CLIOptions{ForceInternalBuildKit: true},
	})

	require.IsType(t, &buildkitStrategy{}, strategy)
	_, err = strategy.(*buildkitStrategy).newDockerClient(context.Background())
	assert.ErrorContains(t, err, "DOCKER_HOST")
}

const (
	processDaemonSock = "unix:///tmp/devsy-test-process-daemon.sock"
	secretMarker      = "devsy-secret-marker"
)

// clearSelectorEnv pins every daemon selector to a known process value so the
// guard comparison never depends on the developer's or CI's environment.
func clearSelectorEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DOCKER_HOST", processDaemonSock)
	t.Setenv("DOCKER_CONTEXT", "")
	t.Setenv("DOCKER_CONFIG", "")
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Setenv("DOCKER_CERT_PATH", "")
}

func TestBuildkitNewDockerClient_ElevatedNamesOnlyDifferingSelectorKeys(t *testing.T) {
	for _, key := range []string{
		"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH",
	} {
		t.Run(key, func(t *testing.T) {
			clearSelectorEnv(t)
			value := secretMarker + "-" + key

			client, err := elevatedDockerStrategy(t, key+"="+value).
				newDockerClient(context.Background())

			require.Error(t, err)
			assert.Nil(t, client)
			assert.ErrorContains(t, err, "overrides "+key+";")
			assert.NotContains(t, err.Error(), secretMarker, "values must not be printed")
			assert.NotContains(t, err.Error(), processDaemonSock, "process values must not be printed")
			for _, other := range []string{
				"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH",
			} {
				if other != key {
					assert.NotContains(t, err.Error(), other, "only differing keys are named")
				}
			}
		})
	}
}

func TestBuildkitNewDockerClient_ElevatedRejectsProviderClearingProcessSelector(t *testing.T) {
	clearSelectorEnv(t)

	_, err := elevatedDockerStrategy(t, "DOCKER_HOST=").newDockerClient(context.Background())

	require.Error(t, err)
	assert.ErrorContains(t, err, "overrides DOCKER_HOST;")
	assert.NotContains(t, err.Error(), processDaemonSock)
}

func TestBuildkitNewDockerClient_ElevatedIgnoresNonSelectorDifferences(t *testing.T) {
	clearSelectorEnv(t)
	t.Setenv("HTTP_PROXY", "http://process-proxy")

	client, err := elevatedDockerStrategy(t,
		"HTTP_PROXY=http://provider-proxy",
		"HOME=/provider/home",
		"DOCKER_BUILDKIT=0",
		"DOCKER_API_VERSION=1.41",
		"DOCKER_HOST="+processDaemonSock,
	).newDockerClient(context.Background())

	require.NoError(t, err)
	require.NotNil(t, client)
	t.Cleanup(func() { _ = client.Close() })
	assert.Equal(t, processDaemonSock, client.DaemonHost(), "elevated Docker keeps the process client")
}

func TestBuildkitNewDockerClient_PodmanAndNerdctlKeepProcessClient(t *testing.T) {
	elevator, err := docker.ElevatorFromName("sudo")
	require.NoError(t, err)

	for _, runtimeName := range []docker.RuntimeName{docker.RuntimePodman, docker.RuntimeNerdctl} {
		rt, err := docker.RuntimeFromName(string(runtimeName))
		require.NoError(t, err)
		for name, elevated := range map[string]*docker.Elevator{"plain": nil, "elevated": elevator} {
			t.Run(string(runtimeName)+"/"+name, func(t *testing.T) {
				clearSelectorEnv(t)
				s := &buildkitStrategy{driver: &dockerDriver{Docker: &docker.DockerHelper{
					DockerCommand: "docker-fake",
					Runtime:       rt,
					Elevator:      elevated,
					Environment: []string{
						"DOCKER_HOST=ssh://user@remote",
						"DOCKER_CONTEXT=remote",
						"DOCKER_CONFIG=/provider/config",
					},
				}}}

				client, err := s.newDockerClient(context.Background())

				require.NoError(t, err, "provider selectors are not rejected for %s", runtimeName)
				require.NotNil(t, client)
				t.Cleanup(func() { _ = client.Close() })
				assert.Equal(t, processDaemonSock, client.DaemonHost(),
					"%s keeps the process-environment client", runtimeName)
			})
		}
	}
}

func TestBuildkitNewDockerClient_UnelevatedDockerUsesProviderInvocation(t *testing.T) {
	clearSelectorEnv(t)
	bin := writeHelperScript(t, t.TempDir(), "docker-fake", "Docker version 24.0.7, build afdd53b")
	s := &buildkitStrategy{driver: &dockerDriver{Docker: &docker.DockerHelper{
		DockerCommand: bin,
		Builder:       docker.DockerBuilderBuildKit,
		Environment:   []string{"DOCKER_HOST=ssh://user@remote"},
	}}}

	client, err := s.newDockerClient(context.Background())

	require.NoError(t, err)
	require.NotNil(t, client)
	t.Cleanup(func() { _ = client.Close() })
	assert.NotEqual(t, processDaemonSock, client.DaemonHost(),
		"the dial-stdio client must not be bound to the process daemon")
}

func TestSelectStrategy_PodmanAndNerdctlUnchanged(t *testing.T) {
	tests := []struct {
		runtime docker.RuntimeName
		want    buildStrategy
	}{
		{docker.RuntimePodman, &dockerBuildxStrategy{}},
		{docker.RuntimeNerdctl, &buildkitStrategy{}},
	}
	for _, tt := range tests {
		t.Run(string(tt.runtime), func(t *testing.T) {
			rt, err := docker.RuntimeFromName(string(tt.runtime))
			require.NoError(t, err)
			o := &buildOrchestrator{driver: &dockerDriver{Docker: &docker.DockerHelper{
				DockerCommand: "docker-fake",
				Runtime:       rt,
				Builder:       docker.DockerBuilderBuildKit,
				Environment:   []string{"DOCKER_HOST=ssh://user@remote"},
			}}}

			strategy := o.selectStrategy(provider.BuildOptions{
				CLIOptions: provider.CLIOptions{ForceInternalBuildKit: true},
			})

			assert.IsType(t, tt.want, strategy)
		})
	}
}
