package docker

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/build"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	// If buildx isn't installed, it falls back to buildkit — both are valid for Docker.
	switch strategy.(type) {
	case *dockerBuildxStrategy:
		// Docker with buildx available
	case *buildkitStrategy:
		// Docker without buildx — still valid, not Podman
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
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := buildDockerBuildxArgs(tt.opts, "")
			assert.Equal(t, tt.wantPull, slices.Contains(args, "--pull"), "args=%v", args)
			assert.Equal(t, tt.wantNoCach, slices.Contains(args, "--no-cache"), "args=%v", args)
		})
	}
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
