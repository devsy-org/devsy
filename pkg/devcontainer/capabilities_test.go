package devcontainer

import (
	"context"
	"errors"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type separateImages struct {
	driver.ImageBackend
	details      *config.ImageDetails
	buildInfo    *config.BuildInfo
	err          error
	request      driver.BuildRequest
	tags, pushes []string
}

func (m *separateImages) InspectImage(_ context.Context, _ string) (*config.ImageDetails, error) {
	return m.details, m.err
}

func (m *separateImages) GetImageTag(_ context.Context, _ string) (string, error) {
	return "backend:tag", m.err
}

func (m *separateImages) BuildDevContainer(
	_ context.Context,
	req driver.BuildRequest,
) (*config.BuildInfo, error) {
	m.request = req
	return m.buildInfo, m.err
}

func (m *separateImages) TagDevContainer(_ context.Context, _, tag string) error {
	m.tags = append(m.tags, tag)
	return m.err
}

func (m *separateImages) PushDevContainer(_ context.Context, image string) error {
	m.pushes = append(m.pushes, image)
	return m.err
}

func TestSeparateImageBackend(t *testing.T) {
	ctx := context.Background()
	backend := &separateImages{
		details:   &config.ImageDetails{ID: "backend-image"},
		buildInfo: &config.BuildInfo{ImageName: "built-image"},
	}
	r := newTestRunner(&mockDriver{})
	r.imageBackend = backend
	details, err := r.inspectImage(ctx, "image")
	require.NoError(t, err)
	assert.Same(t, backend.details, details)
	tag, err := r.getImageTag(ctx, "image")
	require.NoError(t, err)
	assert.Equal(t, "backend:tag", tag)
	result, err := r.executeBuild(
		ctx,
		&buildImageParams{
			parsedConfig: &config.SubstitutedConfig{},
			options:      provider.BuildOptions{},
		},
		"hash",
		"arm64",
	)
	require.NoError(t, err)
	assert.Same(t, backend.buildInfo, result)
	assert.Equal(t, "hash", backend.request.PrebuildHash)
	require.NoError(t, tagAndPushImages(ctx, backend, "registry/repo:prebuild", []string{"extra"}))
	assert.Equal(t, []string{"registry/repo:prebuild", "registry/repo:extra"}, backend.tags)
	assert.Equal(t, backend.tags, backend.pushes)
	backend.err = errors.New("backend unavailable")
	_, err = r.inspectImage(ctx, "image")
	assert.ErrorIs(t, err, backend.err)
	_, err = r.getImageTag(ctx, "image")
	assert.ErrorIs(t, err, backend.err)
	_, err = r.executeBuild(
		ctx,
		&buildImageParams{parsedConfig: &config.SubstitutedConfig{}},
		"hash",
		"arm64",
	)
	assert.ErrorIs(t, err, backend.err)
}

type architectureDriver struct {
	mockDriver
	arch              string
	err               error
	workspaceID       string
	architectureCalls int
}

func (d *architectureDriver) TargetArchitecture(_ context.Context, id string) (string, error) {
	d.workspaceID = id
	d.architectureCalls++
	return d.arch, d.err
}

func TestDeliveryArchitectureUsesRuntime(t *testing.T) {
	for _, name := range []string{
		provider.DockerDriver, provider.AppleDriver, provider.CustomDriver,
		provider.KubernetesDriver, provider.MicrosandboxDriver, "external",
	} {
		t.Run(name, func(t *testing.T) {
			d := &architectureDriver{arch: "arm64"}
			r := newTestRunner(d)
			r.workspaceConfig.Agent.Driver = name
			arch, err := r.deliveryArch(context.Background())
			require.NoError(t, err)
			assert.Equal(t, "arm64", arch)
			assert.Equal(t, r.id, d.workspaceID)
			d.arch = ""
			_, err = r.deliveryArch(context.Background())
			require.ErrorContains(t, err, "target architecture is empty")
			d.err = errors.New("runtime unavailable")
			_, err = r.deliveryArch(context.Background())
			assert.ErrorIs(t, err, d.err)
		})
	}
}

type recreateDriver struct {
	mockDriver
	mode driver.RecreateMode
}

func (d *recreateDriver) RecreateMode() driver.RecreateMode { return d.mode }

func TestRecreatePolicy(t *testing.T) {
	for _, mode := range []driver.RecreateMode{driver.RecreateDelete, driver.RecreateStop, "invalid"} {
		t.Run(string(mode), func(t *testing.T) {
			d := &recreateDriver{
				mode: mode,
				mockDriver: mockDriver{
					findResult: &config.ContainerDetails{
						State: config.ContainerDetailsState{Status: config.ContainerStatusExited},
					},
				},
			}
			r := newTestRunner(d)
			err := r.deleteForRecreate(context.Background())
			if mode == "invalid" {
				require.ErrorContains(t, err, "unsupported recreate mode")
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, mode == driver.RecreateDelete, d.deleteCalled)
			assert.Equal(t, mode == driver.RecreateStop, d.stopCalled)
		})
	}
}

func TestBuildBackendSelection(t *testing.T) {
	r := newTestRunner(&mockDriver{})
	r.workspaceConfig.Agent.Dockerless.Disabled = "true"
	params := &buildImageParams{parsedConfig: &config.SubstitutedConfig{}}
	_, err := r.executeBuild(context.Background(), params, "hash", "amd64")
	require.ErrorContains(t, err, "dockerless fallback is disabled")
	r.imageBackend = &separateImages{err: errors.New("image backend should not run")}
	params.options.ForceDockerless = true
	_, err = r.executeBuild(context.Background(), params, "hash", "amd64")
	require.ErrorContains(t, err, "dockerless fallback is disabled")
	r.imageBackend = nil
	tag, err := r.getImageTag(context.Background(), "image")
	require.NoError(t, err)
	assert.Empty(t, tag)
	_, err = r.Build(context.Background(), provider.BuildOptions{})
	require.ErrorContains(t, err, "requires an image backend")
}

func TestRecreateErrorsPreserveCause(t *testing.T) {
	cause := errors.New("runtime unavailable")
	for _, mode := range []driver.RecreateMode{driver.RecreateDelete, driver.RecreateStop} {
		t.Run(string(mode), func(t *testing.T) {
			d := &recreateDriver{mode: mode, mockDriver: mockDriver{
				findResult: &config.ContainerDetails{
					State: config.ContainerDetailsState{Status: config.ContainerStatusExited},
				},
				deleteErr: cause,
				stopErr:   cause,
			}}
			err := newTestRunner(d).deleteForRecreate(context.Background())
			assert.ErrorIs(t, err, cause)
		})
	}
}

type imageOnlyRuntime struct {
	mockDriver
	params *driver.RunImageDevContainerParams
}

func (d *imageOnlyRuntime) RunImageDevContainer(
	_ context.Context,
	params *driver.RunImageDevContainerParams,
) error {
	d.params = params
	return nil
}

func TestImageRuntimeDoesNotNeedImageBackend(t *testing.T) {
	d := &imageOnlyRuntime{}
	r := newTestRunner(d)
	r.workspaceConfig.Workspace = &provider.Workspace{UID: "workspace-uid"}
	params := &resolveParams{
		parsedConfig:        &config.SubstitutedConfig{Config: &config.DevContainerConfig{}},
		substitutionContext: &config.SubstitutionContext{},
	}
	info := &config.BuildInfo{
		ImageName:     "runtime-image",
		ImageMetadata: &config.ImageMetadataConfig{},
	}
	require.NoError(
		t,
		r.runContainer(context.Background(), params, &config.MergedDevContainerConfig{}, info),
	)
	require.NotNil(t, d.params)
	assert.Equal(t, r.id, d.params.WorkspaceID)
	assert.Equal(t, "runtime-image", d.params.Options.Image)
	assert.Nil(t, r.imageBackend)
}
