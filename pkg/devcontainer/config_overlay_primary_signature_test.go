package devcontainer

import (
	"context"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestPrimaryStructureSignatureControlsOverlayRecreation(t *testing.T) {
	t.Parallel()

	prior := primarySignatureConfig(exampleBaseImage)
	current := config.CloneDevContainerConfig(prior)
	current.Origin = "/different-config/devcontainer.json"
	current.RemoteEnv = map[string]*string{"PRIMARY_RUNTIME_ONLY": new("runtime")}
	current.PostStartCommand = map[string][]string{"": {"echo started"}}
	details := overlayContainerDetails(structuralSignature(prior))
	d := &mockDriver{findResult: details}
	r := newTestRunner(d)

	err := r.checkOverlayRecreation(
		context.Background(),
		&config.SubstitutedConfig{Config: current},
		UpOptions{},
	)
	require.NoError(t, err)
	require.Nil(t, r.overlayExisting)
	require.False(t, d.deleteCalled)
	require.False(t, d.stopCalled)
}

func TestPrimaryStructureSignatureRequiresRecreation(t *testing.T) {
	t.Parallel()

	prior := primarySignatureConfig(exampleBaseImage)
	current := primarySignatureConfig(exampleUpdatedImage)
	details := overlayContainerDetails(structuralSignature(prior))
	d := &mockDriver{findResult: details}
	r := newTestRunner(d)

	err := r.checkOverlayRecreation(
		context.Background(),
		&config.SubstitutedConfig{Config: current},
		UpOptions{},
	)
	require.ErrorContains(t, err, recreateFlag)
	require.Nil(t, r.overlayExisting)
	require.False(t, d.deleteCalled)
	require.False(t, d.stopCalled)
}

func TestPrimaryStructureSignatureTracksRecreation(t *testing.T) {
	t.Parallel()

	prior := primarySignatureConfig(exampleBaseImage)
	current := primarySignatureConfig(exampleUpdatedImage)
	details := overlayContainerDetails(structuralSignature(prior))
	r := newTestRunner(&mockDriver{findResult: details})

	err := r.checkOverlayRecreation(
		context.Background(),
		&config.SubstitutedConfig{Config: current},
		UpOptions{CLIOptions: provider.CLIOptions{Recreate: true}},
	)
	require.NoError(t, err)
	require.Same(t, details, r.overlayExisting)
}

func primarySignatureConfig(image string) *config.DevContainerConfig {
	return &config.DevContainerConfig{
		Origin:         overlayBaseFile,
		ImageContainer: config.ImageContainer{Image: image},
	}
}
