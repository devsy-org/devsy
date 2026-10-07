package devcontainer

import (
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

const overlayCacheService = "cache"

func TestResolveLegacyComposeConfigPreservesPersistedSelection(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	origin := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	prior := &config.DevContainerConfig{
		Origin: origin,
		ComposeContainer: config.ComposeContainer{
			DockerComposeFile: []string{structuralComposeFile, "compose.override.yaml"},
			Service:           structuralService,
			RunServices:       []string{overlayDatabaseService, overlayCacheService},
		},
	}
	r := newRunnerAt(workspace)
	r.workspaceConfig.CLIOptions = provider.CLIOptions{DevContainerImage: exampleUpdatedImage}
	r.workspaceConfig.LastDevContainerConfig = &config.DevContainerConfigWithPath{
		Config: prior,
		Path:   filepath.ToSlash(filepath.Join(".devcontainer", "devcontainer.json")),
	}
	optionsBefore := r.workspaceConfig.CLIOptions

	parsed, err := r.resolveLegacyComposeConfig()
	require.NoError(t, err)
	require.Equal(t, []string{structuralComposeFile, "compose.override.yaml"},
		[]string(parsed.Config.DockerComposeFile))
	require.Equal(t, structuralService, parsed.Config.Service)
	require.Equal(
		t,
		[]string{overlayDatabaseService, overlayCacheService},
		parsed.Config.RunServices,
	)
	require.Empty(t, parsed.Config.Image)
	require.Equal(t, optionsBefore, r.workspaceConfig.CLIOptions)
}

func TestResolveLegacyComposeConfigRequiresSourcePath(t *testing.T) {
	t.Parallel()

	r := newRunnerAt(t.TempDir())
	r.workspaceConfig.LastDevContainerConfig = &config.DevContainerConfigWithPath{
		Config: &config.DevContainerConfig{
			ComposeContainer: config.ComposeContainer{
				DockerComposeFile: []string{structuralComposeFile},
			},
		},
	}

	_, err := r.resolveLegacyComposeConfig()
	require.ErrorContains(t, err, "last successful Compose configuration has no source path")
}
