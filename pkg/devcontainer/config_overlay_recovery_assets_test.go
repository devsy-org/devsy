package devcontainer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/compose"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

const overlayRecoveryMissingDockerfile = "missing.Dockerfile"

func TestRecoverySkipsMissingDockerfileOverlayAssets(t *testing.T) {
	for _, recovering := range []bool{false, true} {
		name := "normal"
		if recovering {
			name = "recovery"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, test := range missingDockerfileOverlayAssets(t) {
				t.Run(test.name, func(t *testing.T) {
					r := newRunnerAt(test.originDir)
					err := r.validateOverlayUpAssets(
						context.Background(),
						test.parsed,
						UpOptions{CLIOptions: provider.CLIOptions{Recovery: recovering}},
					)
					if recovering {
						require.NoError(t, err)
						return
					}
					require.ErrorContains(t, err, test.wantError)
				})
			}
		})
	}
}

type missingDockerfileAssetCase struct {
	name      string
	originDir string
	parsed    *config.SubstitutedConfig
	wantError string
}

func missingDockerfileOverlayAssets(t *testing.T) []missingDockerfileAssetCase {
	t.Helper()
	workspace := t.TempDir()
	origin := filepath.Join(workspace, "devcontainer.json")
	missingDockerfile := &config.SubstitutedConfig{
		Config: &config.DevContainerConfig{
			Origin: origin,
			DockerfileContainer: config.DockerfileContainer{
				Dockerfile: overlayRecoveryMissingDockerfile,
				Context:    ".",
			},
		},
		Overlay: &config.DevContainerConfig{
			DockerfileContainer: config.DockerfileContainer{
				Dockerfile: overlayRecoveryMissingDockerfile,
			},
		},
	}
	require.NoError(t, os.WriteFile(filepath.Join(workspace, structuralDockerfile), nil, 0o600))
	missingContext := &config.SubstitutedConfig{
		Config: &config.DevContainerConfig{
			Origin: origin,
			DockerfileContainer: config.DockerfileContainer{
				Dockerfile: structuralDockerfile,
				Context:    "missing-context",
			},
		},
		Overlay: &config.DevContainerConfig{
			DockerfileContainer: config.DockerfileContainer{
				Dockerfile: structuralDockerfile,
				Context:    "missing-context",
			},
		},
	}
	return []missingDockerfileAssetCase{
		{
			name:      "missing Dockerfile",
			originDir: workspace,
			parsed:    missingDockerfile,
			wantError: "dockerfile not found",
		},
		{
			name:      "missing build context",
			originDir: workspace,
			parsed:    missingContext,
			wantError: "overlay build context",
		},
	}
}

func TestRecoveryStillValidatesComposeOverlayAssets(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	origin := filepath.Join(workspace, "devcontainer.json")
	parsed := &config.SubstitutedConfig{
		Config: &config.DevContainerConfig{
			Origin: origin,
			ComposeContainer: config.ComposeContainer{
				DockerComposeFile: []string{"missing-compose.yaml"},
				Service:           structuralService,
			},
		},
		Overlay: &config.DevContainerConfig{
			ComposeContainer: config.ComposeContainer{
				DockerComposeFile: []string{"missing-compose.yaml"},
				Service:           structuralService,
			},
		},
	}
	d := &overlayRecoveryComposeDriver{}
	r := newRunnerAt(workspace)
	r.driver = d

	err := r.validateOverlayUpAssets(
		context.Background(),
		parsed,
		UpOptions{CLIOptions: provider.CLIOptions{Recovery: true}},
	)
	require.ErrorContains(t, err, "load docker compose project")
}

func TestRecoveryUpReachesFallbackImageInspectionWithMissingOverlayDockerfile(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	primaryPath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	overlayPath := filepath.Join(workspace, "overlay.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(primaryPath), 0o750))
	writeJSONConfig(t, primaryPath, map[string]any{overlayImageKey: exampleBaseImage})
	writeJSONConfig(
		t,
		overlayPath,
		map[string]any{overlayDockerfileKey: overlayRecoveryMissingDockerfile},
	)

	buildFailure := errors.New("fallback image inspection reached")
	backend := &overlayRecoveryImageBackend{
		separateImages: separateImages{err: buildFailure},
	}
	d := &mockDriver{}
	r := newRunnerAt(workspace)
	r.driver = d
	r.imageBackend = backend

	_, err := r.Up(
		context.Background(),
		UpOptions{CLIOptions: provider.CLIOptions{
			ExtraDevContainerPath: overlayPath,
			Recovery:              true,
		}},
		0,
		nil,
	)
	require.ErrorIs(t, err, buildFailure)
	require.Positive(t, backend.inspectCalls)
	require.False(t, d.stopCalled)
	require.False(t, d.deleteCalled)
}

type overlayRecoveryComposeDriver struct{ mockDriver }

func (*overlayRecoveryComposeDriver) ComposeHelper() (*compose.ComposeHelper, error) {
	return &compose.ComposeHelper{Version: testComposeVersion}, nil
}

type overlayRecoveryImageBackend struct {
	separateImages
	inspectCalls int
}

func (b *overlayRecoveryImageBackend) InspectImage(
	_ context.Context,
	_ string,
) (*config.ImageDetails, error) {
	b.inspectCalls++
	return nil, b.err
}
