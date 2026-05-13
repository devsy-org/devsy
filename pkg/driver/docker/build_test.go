package docker

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
