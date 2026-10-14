package devcontainer

import (
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/stretchr/testify/require"
)

func TestImageOverlayClearsInheritedComposeSelectors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	basePath := filepath.Join(root, overlayBaseHashKey, "devcontainer.json")
	overlayPath := filepath.Join(root, "overlay", "devcontainer.json")
	base := parseOverlayTestConfig(t, basePath, map[string]any{
		overlayImageKey:       "base-image",
		overlayComposeFileKey: structuralComposeFile,
		overlayServiceKey:     structuralService,
		overlayRunServicesKey: []string{composeSecretTestServiceName},
	})
	overlay := parseOverlayTestConfig(t, overlayPath, map[string]any{
		overlayImageKey: structuralImage,
	})

	require.NoError(t, applyOverlayPlanningInputs(base, overlay, &config.SubstitutionContext{}))
	require.Equal(t, structuralImage, base.Image)
	require.Empty(t, base.DockerComposeFile)
	require.Empty(t, base.Service)
	require.Nil(t, base.RunServices)
}

func TestDockerfileOverlayClearsOtherSelectorsAndInheritsBuildProperties(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	basePath := filepath.Join(root, overlayBaseHashKey, "devcontainer.json")
	overlayPath := filepath.Join(root, "overlay", "devcontainer.json")
	base := parseOverlayTestConfig(t, basePath, map[string]any{
		overlayImageKey: structuralImage,
		overlayBuildKey: map[string]any{
			overlayDockerfileKey: "./base.Dockerfile",
			overlayContextKey:    "./base-context",
			overlayArgsKey:       map[string]string{overlayBaseBuildArgKey: "inherited-value"},
			overlayTargetKey:     "base-target",
			overlayOptionsKey:    []string{overlayPullOption},
			overlayCacheFromKey:  []string{"inherited-cache"},
		},
		overlayComposeFileKey: structuralComposeFile,
		overlayServiceKey:     structuralService,
		overlayRunServicesKey: []string{composeSecretTestServiceName},
	})
	overlay := parseOverlayTestConfig(t, overlayPath, map[string]any{
		overlayLegacyDockerfileKey: "./overlay.Dockerfile",
	})

	require.NoError(t, applyOverlayPlanningInputs(base, overlay, &config.SubstitutionContext{}))
	require.Equal(
		t,
		expectedOverlayPath(basePath, overlayPath, "./overlay.Dockerfile"),
		base.GetDockerfile(),
	)
	require.Empty(t, base.Image)
	require.Empty(t, base.DockerComposeFile)
	require.Empty(t, base.Service)
	require.Nil(t, base.RunServices)
	require.Equal(t, "./base-context", base.GetContext())
	require.Equal(t, map[string]string{overlayBaseBuildArgKey: "inherited-value"}, base.GetArgs())
	require.Equal(t, "base-target", base.GetTarget())
	require.Equal(t, []string{overlayPullOption}, []string(base.GetOptions()))
	require.Equal(t, []string{"inherited-cache"}, []string(base.GetCacheFrom()))
}
