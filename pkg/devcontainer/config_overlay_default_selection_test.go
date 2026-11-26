package devcontainer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/language"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/require"
)

func TestFallbackDefaultSelectionReusesMatchingContainer(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	options := UpOptions{CLIOptions: provider.CLIOptions{FallbackImage: exampleBaseImage}}
	prior := resolveTestDefaultConfig(t, workspace, options)
	details := overlayContainerDetails(structuralSignature(prior))
	r := newTestRunner(&mockDriver{findResult: details})
	r.localWorkspaceFolder = workspace
	current := resolveTestDefaultConfig(t, workspace, options)

	err := r.checkOverlayRecreation(
		context.Background(),
		&config.SubstitutedConfig{Config: current},
		options,
	)
	require.NoError(t, err)
	require.Nil(t, r.overlayExisting)
}

func TestUpResolvesFallbackBeforeComparingExistingContainerSignature(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	basePath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(basePath), 0o750))
	writeJSONConfig(t, basePath, map[string]any{})
	fallbackImage := exampleBaseImage
	expected := &config.DevContainerConfig{
		Origin:         basePath,
		ImageContainer: config.ImageContainer{Image: fallbackImage},
	}
	details := overlayContainerDetails(structuralSignature(expected))
	d := &defaultSelectionUpDriver{mockDriver: mockDriver{findResult: details}}
	buildFailure := errors.New(overlaySkipBuildError)
	imageBackend := &overlaySkipImageBackend{
		separateImages: separateImages{err: buildFailure},
	}
	r := newRunnerAt(workspace)
	r.driver = d
	r.imageBackend = imageBackend
	options := UpOptions{CLIOptions: provider.CLIOptions{FallbackImage: fallbackImage}}

	_, err := r.Up(context.Background(), options, 0, nil)
	require.ErrorIs(t, err, buildFailure)
	require.Equal(t, 1, imageBackend.inspectCalls)
	require.Equal(t, 2, d.findCalls)
	require.False(t, d.deleteCalled)
	require.False(t, d.stopCalled)
}

type defaultSelectionUpDriver struct {
	mockDriver
	findCalls int
}

func (d *defaultSelectionUpDriver) FindDevContainer(
	_ context.Context,
	_ string,
) (*config.ContainerDetails, error) {
	d.findCalls++
	if d.findCalls == 1 {
		return d.findResult, d.findErr
	}
	return nil, nil
}

func TestChangedFallbackDefaultSelectionRequiresRecreate(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	priorOptions := UpOptions{CLIOptions: provider.CLIOptions{FallbackImage: exampleBaseImage}}
	prior := resolveTestDefaultConfig(t, workspace, priorOptions)
	details := overlayContainerDetails(structuralSignature(prior))
	d := &mockDriver{findResult: details}
	r := newTestRunner(d)
	r.localWorkspaceFolder = workspace
	currentOptions := UpOptions{CLIOptions: provider.CLIOptions{FallbackImage: exampleUpdatedImage}}
	current := resolveTestDefaultConfig(t, workspace, currentOptions)

	err := r.checkOverlayRecreation(
		context.Background(),
		&config.SubstitutedConfig{Config: current},
		currentOptions,
	)
	require.ErrorContains(t, err, recreateFlag)
	require.Nil(t, r.overlayExisting)
	require.False(t, d.deleteCalled)
	require.False(t, d.stopCalled)
}

func TestLanguageDefaultSelectionKeepsMatchingContainer(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	require.NoError(
		t,
		os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package main"), 0o600),
	)
	prior := resolveTestDefaultConfig(t, workspace, UpOptions{})
	require.Equal(t, language.MapConfig[language.Go].Image, prior.Image)
	details := overlayContainerDetails(structuralSignature(prior))
	r := newTestRunner(&mockDriver{findResult: details})
	r.localWorkspaceFolder = workspace
	current := resolveTestDefaultConfig(t, workspace, UpOptions{})

	err := r.checkOverlayRecreation(
		context.Background(),
		&config.SubstitutedConfig{Config: current},
		UpOptions{},
	)
	require.NoError(t, err)
	require.Nil(t, r.overlayExisting)
}

func resolveTestDefaultConfig(
	t *testing.T,
	workspace string,
	options UpOptions,
) *config.DevContainerConfig {
	t.Helper()
	conf := &config.DevContainerConfig{
		Origin: filepath.Join(workspace, "devcontainer.json"),
	}
	r := newTestRunner(&mockDriver{})
	r.localWorkspaceFolder = workspace
	require.NoError(t, r.resolveDefaultContainerConfig(conf, options))
	return conf
}
