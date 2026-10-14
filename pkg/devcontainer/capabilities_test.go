package devcontainer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
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
	for _, mode := range []driver.RecreateMode{
		driver.RecreateDelete, driver.RecreateStop, driver.RecreateOnRun, "invalid",
	} {
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

type validatingImageRuntime struct {
	*mockDriver
	validationErr    error
	validationParams *driver.RunImageDevContainerParams
}

func (d *validatingImageRuntime) ValidateRunImageDevContainer(
	params *driver.RunImageDevContainerParams,
) error {
	d.validationParams = params
	return d.validationErr
}

func TestImageRunValidationPreservesContainerBeforeRecreate(t *testing.T) {
	for _, reject := range []bool{true, false} {
		t.Run(fmt.Sprintf("reject=%t", reject), func(t *testing.T) {
			sentinel := errors.New("unsupported creation settings")
			d := &validatingImageRuntime{mockDriver: &mockDriver{}}
			if reject {
				d.validationErr = sentinel
			}
			r := newTestRunner(d)
			r.workspaceConfig.Workspace = &provider.Workspace{ID: r.id, UID: "workspace-uid"}
			r.imageBackend = &separateImages{details: &config.ImageDetails{ID: "image"}}
			r.reporter = status.Nop()
			p := recreateResolveParams()
			p.parsedConfig.Config.Image = "alpine"
			p.parsedConfig.Raw = config.CloneDevContainerConfig(p.parsedConfig.Config)
			p.substitutionContext = &config.SubstitutionContext{}
			_, _, err := r.buildNewContainerConfig(context.Background(), p)
			if reject {
				require.ErrorIs(t, err, sentinel)
			} else {
				require.NoError(t, err)
			}
			require.NotNil(t, d.validationParams)
			require.Equal(t, r.id, d.validationParams.WorkspaceID)
			require.NotEmpty(t, d.validationParams.Options.Image)
			assert.Equal(t, !reject, d.stopCalled)
			assert.False(t, d.deleteCalled)
		})
	}
}

type dockerDiscoveryRuntime struct {
	*mockDriver
}

func (d *dockerDiscoveryRuntime) DockerHelper() (*docker.DockerHelper, error) {
	return nil, nil
}

func TestWorkspaceDiscoveryUsesRuntimeCapability(t *testing.T) {
	for _, dockerBacked := range []bool{false, true} {
		for _, withImages := range []bool{false, true} {
			t.Run(fmt.Sprintf("docker=%t/images=%t", dockerBacked, withImages), func(t *testing.T) {
				existing := runningContainerDetails()
				base := &mockDriver{findResult: existing}
				var runtimeDriver driver.Driver = base
				if dockerBacked {
					runtimeDriver = &dockerDiscoveryRuntime{mockDriver: base}
				}
				r := newTestRunner(runtimeDriver)
				r.workspaceConfig.Agent.Docker.Path = filepath.Join(t.TempDir(), "missing-docker")
				if withImages {
					r.imageBackend = &separateImages{}
				}
				found, err := r.findExistingDevContainer(context.Background())
				require.NoError(t, err)
				if dockerBacked {
					assert.Nil(t, found)
				} else {
					assert.Same(t, existing, found)
					base.findErr = errors.New("runtime discovery failed")
					_, err = r.findExistingDevContainer(context.Background())
					assert.ErrorIs(t, err, base.findErr)
				}
			})
		}
	}
}

func TestBuildNewContainerConfigRecoveryDisabledWrapsRecoverable(t *testing.T) {
	buildErr := errors.New("docker build failed: exit status 1")
	d := &mockDriver{}
	r := newTestRunner(d)
	r.imageBackend = &separateImages{
		details: &config.ImageDetails{ID: "image"},
		err:     buildErr,
	}
	r.reporter = status.Nop()

	p := recreateResolveParams()
	p.options.Recovery = false
	p.parsedConfig.Config.Image = "alpine"
	p.parsedConfig.Raw = config.CloneDevContainerConfig(p.parsedConfig.Config)
	p.substitutionContext = &config.SubstitutionContext{}

	buildInfo, mergedConfig, err := r.buildNewContainerConfig(context.Background(), p)
	require.Error(t, err)
	require.Nil(t, buildInfo)
	require.Nil(t, mergedConfig)
	require.False(t, r.recovering)

	require.ErrorIs(t, err, clierr.ErrBuildFailedRecoverable)
	require.ErrorIs(t, err, buildErr)
	require.Contains(t, err.Error(), "build image:")
	require.Contains(t, err.Error(), "docker build failed: exit status 1")
}

func TestBuildNewContainerConfigRecoveryEnabledSuccessSetsRecovering(t *testing.T) {
	fallbackBuildInfo := &config.BuildInfo{
		ImageName:     "recovery-image",
		ImageMetadata: &config.ImageMetadataConfig{},
	}

	callCount := 0
	backendMock := &buildCallMockImageBackend{
		separateImages: &separateImages{
			details:   &config.ImageDetails{ID: "recovery-image"},
			buildInfo: fallbackBuildInfo,
		},
		inspectFn: func(ctx context.Context, image string) (*config.ImageDetails, error) {
			callCount++
			if callCount == 1 {
				return nil, errors.New("primary inspect image failed")
			}
			return &config.ImageDetails{ID: image}, nil
		},
	}

	d := &mockDriver{}
	r := newTestRunner(d)
	r.imageBackend = backendMock
	r.reporter = status.Nop()

	p := recreateResolveParams()
	p.options.Recovery = true
	p.options.Recreate = false
	p.parsedConfig.Config.Image = "custom-image"
	p.parsedConfig.Raw = config.CloneDevContainerConfig(p.parsedConfig.Config)
	p.substitutionContext = &config.SubstitutionContext{}

	buildInfo, mergedConfig, err := r.buildNewContainerConfig(context.Background(), p)
	require.NoError(t, err)
	require.NotNil(t, buildInfo)
	require.NotNil(t, mergedConfig)
	require.True(t, r.recovering)
	require.Equal(t, "custom-image", buildInfo.ImageName)
}

func TestBuildNewContainerConfigRecoveryEnabledFailureDoesNotSetRecovering(t *testing.T) {
	primaryErr := errors.New("primary inspect failed")
	recoveryErr := errors.New("recovery inspect failed")

	callCount := 0
	backendMock := &buildCallMockImageBackend{
		separateImages: &separateImages{},
		inspectFn: func(ctx context.Context, image string) (*config.ImageDetails, error) {
			callCount++
			if callCount == 1 {
				return nil, primaryErr
			}
			return nil, recoveryErr
		},
	}

	d := &mockDriver{}
	r := newTestRunner(d)
	r.imageBackend = backendMock
	r.reporter = status.Nop()

	p := recreateResolveParams()
	p.options.Recovery = true
	p.parsedConfig.Config.Image = "custom-image"
	p.parsedConfig.Raw = config.CloneDevContainerConfig(p.parsedConfig.Config)
	p.substitutionContext = &config.SubstitutionContext{}

	buildInfo, mergedConfig, err := r.buildNewContainerConfig(context.Background(), p)
	require.Error(t, err)
	require.Nil(t, buildInfo)
	require.Nil(t, mergedConfig)
	require.False(t, r.recovering)
	require.ErrorIs(t, err, recoveryErr)
	require.Contains(t, err.Error(), "build recovery image")
	require.Contains(t, err.Error(), "primary inspect failed")
}

func TestBuildNewContainerConfigRecreateNonValidatorDeletesExisting(t *testing.T) {
	d := &mockDriver{}
	r := newTestRunner(d)
	r.workspaceConfig.Workspace = &provider.Workspace{ID: r.id, UID: "workspace-uid"}
	r.imageBackend = &separateImages{
		details:   &config.ImageDetails{ID: "image"},
		buildInfo: &config.BuildInfo{ImageName: "built-image"},
	}
	r.reporter = status.Nop()

	p := recreateResolveParams()
	p.options.Recreate = true
	p.parsedConfig.Config.Image = "alpine"
	p.parsedConfig.Raw = config.CloneDevContainerConfig(p.parsedConfig.Config)
	p.substitutionContext = &config.SubstitutionContext{}

	buildInfo, mergedConfig, err := r.buildNewContainerConfig(context.Background(), p)
	require.NoError(t, err)
	require.NotNil(t, buildInfo)
	require.NotNil(t, mergedConfig)

	assert.True(t, d.stopCalled)
	assert.False(t, d.deleteCalled)
}

type buildCallMockImageBackend struct {
	*separateImages
	buildFn   func(ctx context.Context, req driver.BuildRequest) (*config.BuildInfo, error)
	inspectFn func(ctx context.Context, image string) (*config.ImageDetails, error)
}

func (b *buildCallMockImageBackend) InspectImage(
	ctx context.Context,
	image string,
) (*config.ImageDetails, error) {
	if b.inspectFn != nil {
		return b.inspectFn(ctx, image)
	}
	return b.separateImages.InspectImage(ctx, image)
}

func (b *buildCallMockImageBackend) BuildDevContainer(
	ctx context.Context,
	req driver.BuildRequest,
) (*config.BuildInfo, error) {
	if b.buildFn != nil {
		return b.buildFn(ctx, req)
	}
	return b.separateImages.BuildDevContainer(ctx, req)
}
