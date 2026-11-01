package devcontainer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/metadata"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/stretchr/testify/require"
)

const (
	detailsTestUser       = "vscode"
	detailsTestArgument   = "literal arg"
	detailsTestEntrypoint = "/entry"
	detailsTestCommand    = "serve"
)

type imageDetailsDriver struct {
	failingArchitectureDriver
	details  *config.ImageDetails
	err      error
	requests []driver.ImageDetailsRequest
}

func (d *imageDetailsDriver) ImageDetails(
	req driver.ImageDetailsRequest,
) (*config.ImageDetails, error) {
	d.requests = append(d.requests, req)
	if d.err != nil {
		return nil, d.err
	}
	if d.details != nil && req.UnsupportedReason != "" {
		return nil, errors.New(req.UnsupportedReason)
	}
	return d.details, nil
}

func TestImageDetailsSnapshot(t *testing.T) {
	d := &imageDetailsDriver{
		failingArchitectureDriver: failingArchitectureDriver{err: errors.New("inspect called")},
		details: &config.ImageDetails{
			Config: config.ImageDetailsConfig{
				User:       detailsTestUser,
				Env:        []string{"A=b=c"},
				Entrypoint: []string{detailsTestArgument},
				Cmd:        []string{"cmd arg"},
				Labels: map[string]string{
					"devcontainer.metadata": `[{"remoteUser":"dev","containerEnv":{"FOO":"bar"}}]`,
				},
			},
		},
	}
	r := newTestRunner(d)
	parsed := &config.SubstitutedConfig{
		Config: &config.DevContainerConfig{
			ImageContainer: config.ImageContainer{Image: overlayImageField},
		},
	}
	require.NoError(t, r.prepareImageDetails(parsed, UpOptions{}, false))
	d.details = nil
	info, err := r.getImageBuildInfoFromImage(
		context.Background(),
		&config.SubstitutionContext{},
		overlayImageField,
	)
	require.NoError(t, err)
	require.Equal(t, detailsTestUser, info.User)
	require.Equal(t, []string{"A=b=c"}, info.ImageDetails.Config.Env)
	require.Equal(t, []string{detailsTestArgument}, info.ImageDetails.Config.Entrypoint)
	require.Equal(t, []string{"cmd arg"}, info.ImageDetails.Config.Cmd)
	require.Equal(t, "dev", info.Metadata.Config[0].RemoteUser)
	require.Equal(t, "bar", info.Metadata.Config[0].ContainerEnv["FOO"])
	_, err = r.getImageBuildInfoFromImage(
		context.Background(),
		&config.SubstitutionContext{},
		"other",
	)
	require.ErrorContains(t, err, "do not match")
	require.Len(t, d.requests, 1)
}

func TestImageDetailsScope(t *testing.T) {
	cases := []struct {
		name             string
		modify           func(*config.DevContainerConfig, *UpOptions)
		defaultSelection bool
	}{
		{
			"build",
			func(c *config.DevContainerConfig, _ *UpOptions) { c.Build = &config.ConfigBuildOptions{} },
			false,
		},
		{
			"compose",
			func(c *config.DevContainerConfig, _ *UpOptions) { c.DockerComposeFile = types.StrArray{"compose.yml"} },
			false,
		},
		{
			"container",
			func(c *config.DevContainerConfig, _ *UpOptions) { c.ContainerID = "existing" },
			false,
		},
		{"features", func(c *config.DevContainerConfig, _ *UpOptions) {
			c.Features = map[string]any{"feature": map[string]any{}}
		}, false},
		{
			"dockerless",
			func(_ *config.DevContainerConfig, o *UpOptions) { o.ForceDockerless = true },
			false,
		},
		{"recovery", func(_ *config.DevContainerConfig, o *UpOptions) { o.Recovery = true }, false},
		{
			"prebuild",
			func(_ *config.DevContainerConfig, o *UpOptions) { o.PrebuildRepositories = []string{"repo"} },
			false,
		},
		{"export", func(_ *config.DevContainerConfig, o *UpOptions) { o.Output = "file" }, false},
		{"default", func(_ *config.DevContainerConfig, _ *UpOptions) {}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := &imageDetailsDriver{details: &config.ImageDetails{}}
			r := newTestRunner(d)
			c := &config.DevContainerConfig{
				ImageContainer: config.ImageContainer{Image: overlayImageField},
			}
			o := UpOptions{}
			tc.modify(c, &o)
			require.Error(
				t,
				r.prepareImageDetails(&config.SubstitutedConfig{Config: c}, o, tc.defaultSelection),
			)
			require.NotEmpty(t, d.requests[0].UnsupportedReason)
		})
	}
}

func TestImageDetailsRejectsCLIAddedFeatures(t *testing.T) {
	c := &config.DevContainerConfig{ImageContainer: config.ImageContainer{Image: overlayImageField}}
	require.NoError(t, mergeAdditionalFeatures(c, `{"example/feature:1":{}}`))
	d := &imageDetailsDriver{details: &config.ImageDetails{}}
	err := newTestRunner(
		d,
	).prepareImageDetails(&config.SubstitutedConfig{Config: c}, UpOptions{}, false)
	require.ErrorContains(t, err, "features")
}

func TestImageDetailsUnsetAndError(t *testing.T) {
	d := &imageDetailsDriver{
		failingArchitectureDriver: failingArchitectureDriver{err: errors.New("inspect called")},
	}
	r := newTestRunner(d)
	parsed := &config.SubstitutedConfig{
		Config: &config.DevContainerConfig{
			ImageContainer: config.ImageContainer{Image: overlayImageField},
		},
	}
	require.NoError(t, r.prepareImageDetails(parsed, UpOptions{}, false))
	_, err := r.getImageBuildInfoFromImage(
		context.Background(),
		&config.SubstitutionContext{},
		overlayImageField,
	)
	require.ErrorContains(t, err, "inspect called")
	d.err = errors.New("bad file")
	require.ErrorContains(t, r.prepareImageDetails(parsed, UpOptions{}, false), "bad file")
	_, err = r.Build(context.Background(), provider.BuildOptions{})
	require.ErrorContains(t, err, "bad file")
	require.Equal(t, "standalone workspace build", d.requests[len(d.requests)-1].UnsupportedReason)
}

type imageDetailsUpDriver struct {
	imageDetailsDriver
	startErr       error
	shutdownAction string
	provisionCalls int
	runCalls       int
}

func (d *imageDetailsUpDriver) ManagedLifecycle() bool { return true }

func (d *imageDetailsUpDriver) SetLifecycleShutdownAction(
	action string,
) {
	d.shutdownAction = action
}

func (d *imageDetailsUpDriver) StartDevContainer(
	context.Context,
	string,
) error {
	return d.startErr
}

func (d *imageDetailsUpDriver) ProvisioningPreflight(context.Context) error {
	d.provisionCalls++
	return nil
}

func (d *imageDetailsUpDriver) RunDevContainer(context.Context, string, *driver.RunOptions) error {
	d.runCalls++
	return nil
}

func imageDetailsUpRunner(t *testing.T, d *imageDetailsUpDriver, body string) *runner {
	t.Helper()
	workspace := t.TempDir()
	path := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	r := newRunnerAt(workspace)
	r.driver = d
	return r
}

func TestImageDetailsInvalidUpPreservesWorkspace(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, recreate := range []bool{false, true} {
			t.Run(fmt.Sprintf("existing=%t/recreate=%t", existing, recreate), func(t *testing.T) {
				d := &imageDetailsUpDriver{
					imageDetailsDriver: imageDetailsDriver{err: errors.New("invalid sidecar")},
				}
				if existing {
					d.findResult = runningContainerDetails()
				}
				r := imageDetailsUpRunner(t, d, `{"image":"example/image"}`)
				_, err := r.Up(
					context.Background(),
					UpOptions{CLIOptions: provider.CLIOptions{Recreate: recreate}},
					0,
					nil,
				)
				require.ErrorContains(t, err, "invalid sidecar")
				require.Len(t, d.requests, 1)
				require.Equal(t, "example/image", d.requests[0].ImageName)
				require.Zero(t, d.provisionCalls)
				require.Zero(t, d.runCalls)
				require.False(t, d.stopCalled)
				require.False(t, d.deleteCalled)
			})
		}
	}
}

func TestImageDetailsExistingUpUsesSavedMetadata(t *testing.T) {
	stop := errors.New("start intercepted")
	d := &imageDetailsUpDriver{imageDetailsDriver: imageDetailsDriver{details: &config.ImageDetails{
		Config: config.ImageDetailsConfig{
			Labels: map[string]string{
				metadata.ImageMetadataLabel: `[{"remoteUser":"new-user","shutdownAction":"none"}]`,
			},
		},
	}}, startErr: stop}
	d.findResult = runningContainerDetails()
	d.findResult.State.Status = config.ContainerStatusExited
	d.findResult.Config.Labels[metadata.ImageMetadataLabel] = `[{"remoteUser":"saved-user",` +
		`"shutdownAction":"stopContainer","containerEnv":{"FROM":"saved"}}]`
	r := imageDetailsUpRunner(t, d, `{"image":"example/image"}`)
	_, err := r.Up(context.Background(), UpOptions{}, 0, nil)
	require.ErrorIs(t, err, stop)
	require.Len(t, d.requests, 1)
	require.Equal(t, "stopContainer", d.shutdownAction)
	require.Zero(t, d.runCalls)
	require.Zero(t, d.provisionCalls)
	require.False(t, d.stopCalled)
	require.False(t, d.deleteCalled)
	parsed, substitution, err := r.getSubstitutedConfig(provider.CLIOptions{})
	require.NoError(t, err)
	merged, err := r.mergeExistingContainerConfig(
		context.Background(),
		d.findResult,
		&resolveParams{parsedConfig: parsed, substitutionContext: substitution},
	)
	require.NoError(t, err)
	require.Equal(t, "saved-user", merged.RemoteUser)
	require.Equal(t, "saved", merged.ContainerEnv["FROM"])
}

func TestImageDetailsRunOptionsPrecedence(t *testing.T) {
	no := false
	d := &imageDetailsDriver{
		failingArchitectureDriver: failingArchitectureDriver{err: errors.New("inspect called")},
		details: &config.ImageDetails{Config: config.ImageDetailsConfig{
			User:       "image-user",
			Env:        []string{"IMAGE=a=b"},
			Entrypoint: []string{detailsTestEntrypoint, detailsTestArgument},
			Cmd:        []string{detailsTestCommand, "x y"},
			Labels: map[string]string{
				metadata.ImageMetadataLabel: `[{"remoteUser":"metadata-user",` +
					`"containerUser":"metadata-container","containerEnv":{"FROM":"metadata",` +
					`"INHERITED":"yes"},"postCreateCommand":"echo metadata"}]`,
			},
		}},
	}
	r := newTestRunner(d)
	r.imageBackend = &separateImages{err: errors.New("unexpected inspection")}
	c := &config.DevContainerConfig{ImageContainer: config.ImageContainer{Image: overlayImageField}}
	c.RemoteUser = "explicit-remote"
	c.ContainerUser = "explicit-container"
	c.ContainerEnv = map[string]string{
		"FROM":     "explicit",
		"EXPLICIT": "value",
		"EXPANDED": "${containerEnv:IMAGE}",
	}
	c.OverrideCommand = &no
	parsed := &config.SubstitutedConfig{Config: c, Raw: c}
	sub := &config.SubstitutionContext{}
	require.NoError(t, r.prepareImageDetails(parsed, UpOptions{}, false))
	merged, options := detailsTestRunOptions(t, r, parsed, sub)
	control := newTestRunner(&mockDriver{})
	control.imageBackend = &separateImages{details: d.details}
	_, controlOptions := detailsTestRunOptions(t, control, parsed, sub)
	require.Equal(t, controlOptions, options)
	require.Equal(t, "explicit-container", options.User)
	require.Equal(t, "explicit-remote", options.RemoteUser)
	require.Equal(t, "value", options.Env["EXPLICIT"])
	require.Equal(t, "yes", options.Env["INHERITED"])
	require.Equal(t, "a=b", options.Env["EXPANDED"])
	require.Equal(t, "/bin/sh", options.Entrypoint)
	require.True(t, options.ImageEntrypoint)
	require.Equal(
		t,
		[]string{detailsTestEntrypoint, detailsTestArgument, detailsTestCommand, "x y"},
		options.Cmd[len(options.Cmd)-4:],
	)
	require.Equal(t, []types.LifecycleHook{{"": {"echo metadata"}}}, merged.PostCreateCommands)

	c.RemoteUser, c.ContainerUser = "", ""
	c.ContainerEnv = nil
	parsed = &config.SubstitutedConfig{Config: c, Raw: c}
	_, options = detailsTestRunOptions(t, r, parsed, sub)
	require.Equal(t, "metadata-container", options.User)
	require.Equal(t, "metadata-user", options.RemoteUser)
	require.Equal(t, "metadata", options.Env["FROM"])
}

func detailsTestRunOptions(
	t *testing.T,
	r *runner,
	parsed *config.SubstitutedConfig,
	sub *config.SubstitutionContext,
) (*config.MergedDevContainerConfig, *driver.RunOptions) {
	t.Helper()
	build, err := r.build(context.Background(), parsed, sub, provider.BuildOptions{})
	require.NoError(t, err)
	merged, err := config.MergeConfiguration(parsed.Config, build.ImageMetadata.Config)
	require.NoError(t, err)
	options, err := r.getRunOptions(merged, sub, build, false)
	require.NoError(t, err)
	return merged, options
}

type imageDetailsRecreateDriver struct{ imageDetailsDriver }

func (*imageDetailsRecreateDriver) RecreateMode() driver.RecreateMode { return driver.RecreateOnRun }

func TestImageDetailsRecreateAdoptsNewMetadata(t *testing.T) {
	d := &imageDetailsRecreateDriver{
		imageDetailsDriver{details: &config.ImageDetails{Config: config.ImageDetailsConfig{
			User: "new-image-user",
			Labels: map[string]string{
				metadata.ImageMetadataLabel: `[{"remoteUser":"new-remote","containerEnv":{"VERSION":"new"}}]`,
			},
		}}},
	}
	r := newTestRunner(d)
	c := &config.DevContainerConfig{ImageContainer: config.ImageContainer{Image: overlayImageField}}
	parsed := &config.SubstitutedConfig{Config: c, Raw: c}
	options := UpOptions{CLIOptions: provider.CLIOptions{Recreate: true}}
	require.NoError(t, r.prepareImageDetails(parsed, options, false))
	build, merged, err := r.buildNewContainerConfig(
		context.Background(),
		&resolveParams{
			parsedConfig:        parsed,
			substitutionContext: &config.SubstitutionContext{},
			options:             options,
		},
	)
	require.NoError(t, err)
	require.Equal(t, "new-remote", merged.RemoteUser)
	require.Equal(t, "new", merged.ContainerEnv["VERSION"])
	require.Equal(t, "new-image-user", build.ImageDetails.Config.User)
	require.False(t, d.stopCalled)
	require.False(t, d.deleteCalled)
}

func TestImageDetailsImageUserAndOverrideCommandDefaults(t *testing.T) {
	d := &imageDetailsDriver{details: &config.ImageDetails{Config: config.ImageDetailsConfig{
		User:       "image-user",
		Entrypoint: []string{detailsTestEntrypoint},
		Cmd:        []string{detailsTestCommand},
	}}}
	r := newTestRunner(d)
	c := &config.DevContainerConfig{ImageContainer: config.ImageContainer{Image: overlayImageField}}
	parsed := &config.SubstitutedConfig{Config: c, Raw: c}
	sub := &config.SubstitutionContext{}
	require.NoError(t, r.prepareImageDetails(parsed, UpOptions{}, false))
	build, err := r.build(context.Background(), parsed, sub, provider.BuildOptions{})
	require.NoError(t, err)
	merged, err := config.MergeConfiguration(c, build.ImageMetadata.Config)
	require.NoError(t, err)
	options, err := r.getRunOptions(merged, sub, build, false)
	require.NoError(t, err)
	require.Equal(t, "image-user", options.User)
	require.Equal(t, "image-user", options.RemoteUser)
	require.False(t, options.ImageEntrypoint)
}

func TestImageDetailsDefaultSelectionRejectedBeforeDetection(t *testing.T) {
	d := &imageDetailsUpDriver{
		imageDetailsDriver: imageDetailsDriver{details: &config.ImageDetails{}},
	}
	r := imageDetailsUpRunner(t, d, `{}`)
	_, err := r.Up(context.Background(), UpOptions{}, 0, nil)
	require.ErrorContains(t, err, "explicitly selected image")
	require.Len(t, d.requests, 1)
	require.Zero(t, d.runCalls)
	require.Zero(t, d.provisionCalls)
}

func TestImageDetailsUpRejectsEffectiveFeatures(t *testing.T) {
	for _, overlay := range []bool{false, true} {
		t.Run(fmt.Sprintf("overlay=%t", overlay), func(t *testing.T) {
			d := &imageDetailsUpDriver{
				imageDetailsDriver: imageDetailsDriver{details: &config.ImageDetails{}},
			}
			r := imageDetailsUpRunner(t, d, `{"image":"example/image"}`)
			o := UpOptions{}
			if overlay {
				path := filepath.Join(r.localWorkspaceFolder, "overlay.json")
				require.NoError(
					t,
					os.WriteFile(path, []byte(`{"features":{"example/feature:1":{}}}`), 0o600),
				)
				o.ExtraDevContainerPath = path
			} else {
				o.AdditionalFeatures = `{"example/feature:1":{}}`
			}
			_, err := r.Up(context.Background(), o, 0, nil)
			require.ErrorContains(t, err, "features")
			require.Len(t, d.requests, 1)
			require.Zero(t, d.runCalls)
			require.Zero(t, d.provisionCalls)
			require.False(t, d.stopCalled)
			require.False(t, d.deleteCalled)
		})
	}
}
