package devcontainer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/devsy-org/devsy/pkg/compose"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/metadata"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

const (
	composeFeatureTestID     = "feature:1"
	composeTestWorkspace     = "test-workspace"
	composeTestFeatureDigest = "v1:inputs"
	composeTestImageID       = "sha256:creation-image"
	composeTestProject       = "original"
	composeTestEnvFlag       = "--env-file"
	composeTestOverlayHook   = "overlay"
	composeEmptyMetadata     = `[{}]`
)

func TestComposeMetadataContract(t *testing.T) {
	value := `[{"postStartCommand":"echo feature"},{}]`
	details := &config.ContainerDetails{
		ImageID: composeTestImageID,
		Config: config.ContainerDetailsConfig{Labels: map[string]string{
			metadata.ImageMetadataLabel: value,
			composeContractLabel: marshalConfigValue(
				t,
				composeContract{
					Version:   1,
					Workspace: composeTestWorkspace,
					Image:     composeTestImageID,
					Metadata:  metadataDigest(value),
					Features:  composeTestFeatureDigest,
				},
			),
		}},
	}
	require.NoError(t, composeMetadataError(details))
	details.ImageID = "sha256:other-image"
	require.ErrorContains(t, composeMetadataError(details), "image does not match")
	details.ImageID = "creation-image"
	require.NoError(t, composeMetadataError(details))
	details.ImageID = composeTestImageID
	for _, invalidContract := range []string{"null", "[]", "{", `{"version":2}`, `{"version":1}`} {
		details.Config.Labels[composeContractLabel] = invalidContract
		require.Error(t, composeMetadataError(details))
	}
	details.Config.Labels[composeContractLabel] = marshalConfigValue(t, composeContract{
		Version:   1,
		Workspace: composeTestWorkspace,
		Image:     composeTestImageID,
		Metadata:  metadataDigest(value),
		Features:  composeTestFeatureDigest,
	})
	for _, invalid := range []string{"", `[]`, `null`, `[null]`, `{`, composeEmptyMetadata} {
		t.Run(invalid, func(t *testing.T) {
			details.Config.Labels[metadata.ImageMetadataLabel] = invalid
			require.Error(t, composeMetadataError(details))
		})
	}
	details.Config.Labels = map[string]string{overlayStructureLabel: "v1:legacy"}
	require.Error(t, composeMetadataError(details))
	details.Config.Labels[metadata.ImageMetadataLabel] = `{"postStartCommand":"echo legacy"}`
	require.NoError(t, composeMetadataError(details))
	details.Config.Labels = nil
	require.NoError(t, composeMetadataError(details))
}

func TestComposeContractBindsSelectedManifest(t *testing.T) {
	value := composeEmptyMetadata
	contract := composeContract{
		Version:   1,
		Workspace: composeTestWorkspace,
		Image:     composeTestImageID,
		Manifest:  "sha256:selected",
		Metadata:  metadataDigest(value),
		Features:  composeTestFeatureDigest,
	}
	details := &config.ContainerDetails{
		ImageID:                 composeTestImageID,
		ImageManifestDescriptor: &ocispec.Descriptor{Digest: "sha256:selected"},
		Config: config.ContainerDetailsConfig{Labels: map[string]string{
			metadata.ImageMetadataLabel: value,
			composeContractLabel:        marshalConfigValue(t, contract),
		}},
	}
	require.NoError(t, composeMetadataError(details))
	details.ImageManifestDescriptor.Digest = "sha256:other-platform"
	require.ErrorContains(t, composeMetadataError(details), "image manifest does not match")
	details.ImageManifestDescriptor = nil
	require.ErrorContains(t, composeMetadataError(details), "image manifest does not match")
}

func TestComposeFeatureRecreation(t *testing.T) {
	base := &config.DevContainerConfig{
		ComposeContainer: config.ComposeContainer{DockerComposeFile: []string{"compose.yaml"}},
		DevContainerConfigBase: config.DevContainerConfigBase{
			Features: map[string]any{
				composeFeatureTestID: map[string]any{overlayVersionKey: "1", "enabled": true},
			},
			OverrideFeatureInstallOrder: []string{composeFeatureTestID, "other:1"},
		},
	}
	signature, err := composeFeatureSignature(base)
	require.NoError(t, err)
	details := &config.ContainerDetails{
		ImageID: composeTestImageID,
		Config: config.ContainerDetailsConfig{
			Labels: map[string]string{
				composeContractLabel: marshalConfigValue(
					t,
					composeContract{
						Version:   1,
						Workspace: composeTestWorkspace,
						Image:     composeTestImageID,
						Metadata:  metadataDigest(composeEmptyMetadata),
						Features:  signature,
					},
				),
				metadata.ImageMetadataLabel: composeEmptyMetadata,
			},
		},
	}
	r := newTestRunner(&mockDriver{})
	parsed := &config.SubstitutedConfig{Config: config.CloneDevContainerConfig(base)}
	check := func(options UpOptions) error { return r.checkComposeFeatureRecreation(details, parsed, options) }
	parsed.Config.Features[composeFeatureTestID] = map[string]any{
		"enabled":         true,
		overlayVersionKey: "1",
	}
	require.NoError(t, check(UpOptions{}))
	for _, mutate := range []func(*config.DevContainerConfig){
		func(c *config.DevContainerConfig) {
			c.Features[composeFeatureTestID] = map[string]any{overlayVersionKey: "2"}
		},
		func(c *config.DevContainerConfig) { delete(c.Features, composeFeatureTestID) },
		func(c *config.DevContainerConfig) { c.Features["feature:2"] = map[string]any{} },
		func(c *config.DevContainerConfig) {
			c.OverrideFeatureInstallOrder = []string{"other:1", composeFeatureTestID}
		},
	} {
		parsed.Config = config.CloneDevContainerConfig(base)
		mutate(parsed.Config)
		require.ErrorContains(t, check(UpOptions{}), "--recreate")
		require.NoError(t, check(UpOptions{NoBuild: true}))
		require.NoError(t, check(UpOptions{
			CLIOptions: provider.CLIOptions{Recreate: true},
		}))
	}
	delete(details.Config.Labels, composeContractLabel)
	r.workspaceConfig.LastDevContainerConfig = &config.DevContainerConfigWithPath{Config: base}
	require.ErrorContains(t, check(UpOptions{}), "--recreate")
	r.workspaceConfig.LastDevContainerConfig = nil
	require.NoError(t, check(UpOptions{}))
}

func TestComposeUpOverridePreservesEffectiveLifecycleMetadata(t *testing.T) {
	parsed, imageMetadata, baseFile := composeMetadataFixture(t)
	r := newTestRunner(&mockDriver{})
	r.workspaceConfig.Origin = filepath.Dir(baseFile)
	r.workspaceConfig.CLIOptions.GPUAvailability = "false"
	file, err := r.generateComposeUpOverride(context.Background(), &composeOverrideParams{
		startParams: &startContainerParams{
			parsedConfig:  parsed,
			composeHelper: &compose.ComposeHelper{},
		},
		composeService: &composetypes.ServiceConfig{
			Name:  composeSecretTestServiceName,
			Image: "test:base",
		},
	}, composeExtendResult{imageMetadata: imageMetadata}, &config.ImageDetails{ID: composeTestImageID})
	require.NoError(t, err)
	project, err := compose.LoadDockerComposeProject(
		context.Background(),
		[]string{baseFile, file.path},
		nil,
	)
	require.NoError(t, err)
	labels := project.Services[composeSecretTestServiceName].Labels
	require.Equal(t, composeTestImageID, project.Services[composeSecretTestServiceName].Image)
	require.Equal(
		t,
		composetypes.PullPolicyNever,
		project.Services[composeSecretTestServiceName].PullPolicy,
	)
	require.Equal(t, "retained", labels["user"])
	details := &config.ContainerDetails{
		ImageID: composeTestImageID,
		Config:  config.ContainerDetailsConfig{Labels: labels},
	}
	require.NoError(t, verifyCreatedComposeContainer(details, file.contract))
	decoded, err := metadata.GetImageMetadataFromContainer(details, &config.SubstitutionContext{})
	require.NoError(t, err)
	require.Len(t, decoded.Raw, 3)
	require.Equal(t, parsed.Overlay.PostStartCommand, decoded.Raw[0].PostStartCommand)
	require.NotContains(t, labels[metadata.ImageMetadataLabel], "private-feature-option")
	require.NotContains(t, labels[metadata.ImageMetadataLabel], "host-only-initialization")
	require.Equal(t, types.LifecycleHook{"": {"echo feature"}}, decoded.Raw[1].PostStartCommand)
	require.Equal(t, parsed.Config.PostStartCommand, decoded.Raw[2].PostStartCommand)
	delete(labels, composeContractLabel)
	require.Error(t, verifyCreatedComposeContainer(details, file.contract))
}

func TestComposeResumeCannotFallBackToBuild(t *testing.T) {
	r := newTestRunner(&mockDriver{})
	params := &composeContainerParams{
		project: &composetypes.Project{Name: composeTestProject},
		runParams: &runContainerParams{
			options:      UpOptions{NoBuild: true},
			parsedConfig: &config.SubstitutedConfig{Config: &config.DevContainerConfig{}},
		},
	}
	_, err := r.resumeComposeContainer(context.Background(), params, nil)
	require.ErrorContains(t, err, "no reusable container")
	details := &config.ContainerDetails{
		ImageID: composeTestImageID,
		Config: config.ContainerDetailsConfig{Labels: map[string]string{
			ConfigFilesLabel: filepath.Join(t.TempDir(), "missing.yaml"),
		}},
	}
	_, err = r.resumeComposeContainer(context.Background(), params, details)
	require.ErrorContains(t, err, "saved project files are unavailable")
	_, err = r.startContainer(
		context.Background(),
		&startContainerParams{options: UpOptions{NoBuild: true}},
	)
	require.ErrorContains(t, err, "cannot build or recreate")
}

func TestComposeResumePreservesSavedInvocation(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")
	envFile := filepath.Join(dir, "compose.env")
	require.NoError(t, os.WriteFile(file, []byte("services: {}"), 0o600))
	require.NoError(t, os.WriteFile(envFile, []byte("TEST=original"), 0o600))
	invocation := composeInvocation{Version: 1, Directory: dir, Args: []string{
		composeTestEnvFlag, envFile, "-f", file,
	}}
	details := &config.ContainerDetails{
		ImageID: composeTestImageID,
		Config: config.ContainerDetailsConfig{Labels: map[string]string{
			pkgconfig.ComposeProjectLabel: composeTestProject,
			ConfigFilesLabel:              file,
			composeInvocationLabel:        marshalConfigValue(t, invocation),
		}},
	}
	args, err := composeResumeArgs(details, composeTestProject)
	require.NoError(t, err)
	require.Equal(t, []string{
		composeProjectNameFlag,
		composeTestProject,
		composeProjectDirFlag,
		dir,
		composeTestEnvFlag,
		envFile,
		"-f",
		file,
	}, args)
	details.Config.Labels[composeInvocationLabel] = `{"version":2}`
	_, err = composeResumeArgs(details, composeTestProject)
	require.Error(t, err)
}

func TestComposeResumeUsesWorkspaceIdentity(t *testing.T) {
	details := &config.ContainerDetails{
		ID: testContainerID,
		Config: config.ContainerDetailsConfig{Labels: map[string]string{
			pkgconfig.ComposeProjectLabel: composeTestProject,
			pkgconfig.ComposeServiceLabel: composeSecretTestServiceName,
		}},
	}
	driver := &mockDriver{findResult: details}
	r := newTestRunner(driver)
	parsed := &config.SubstitutedConfig{Config: &config.DevContainerConfig{
		ComposeContainer: config.ComposeContainer{Service: "edited-service"},
	}}
	runParams := &runContainerParams{parsedConfig: parsed, options: UpOptions{NoBuild: true}}
	params, found, err := r.composeResumeTarget(
		context.Background(),
		runParams,
		&compose.ComposeHelper{},
	)
	require.NoError(t, err)
	require.Same(t, details, found)
	require.Equal(t, composeTestProject, params.project.Name)
	require.Equal(t, composeSecretTestServiceName, params.runParams.parsedConfig.Config.Service)
	require.Equal(t, "edited-service", parsed.Config.Service)
	require.True(t, params.runParams.options.NoBuild)
	driver.findResult = nil
	_, _, err = r.composeResumeTarget(context.Background(), runParams, &compose.ComposeHelper{})
	require.ErrorContains(t, err, "no reusable container")
}

func composeMetadataError(details *config.ContainerDetails) error {
	_, err := verifiedComposeContract(details)
	return err
}

func TestComposeIdentityIsInstanceBound(t *testing.T) {
	r := newTestRunner(&mockDriver{})
	contract := composeContract{
		Version:   1,
		Workspace: r.id, Image: composeTestImageID,
		Metadata: "v1:metadata",
		Features: composeTestFeatureDigest,
	}
	labels := map[string]string{
		composeContractLabel:          marshalConfigValue(t, contract),
		pkgconfig.ComposeProjectLabel: composeTestProject,
		pkgconfig.ComposeServiceLabel: composeSecretTestServiceName,
		pkgconfig.DevcontainerIDLabel: r.id,
	}
	details := &config.ContainerDetails{
		ImageID: composeTestImageID,
		Config:  config.ContainerDetailsConfig{Labels: labels},
	}
	verify := func() error {
		return r.verifyComposeIdentity(details, composeTestProject, composeSecretTestServiceName)
	}
	require.NoError(t, verify())
	for _, key := range []string{pkgconfig.ComposeProjectLabel, pkgconfig.ComposeServiceLabel} {
		original := labels[key]
		labels[key] = "other"
		require.Error(t, verify())
		labels[key] = original
	}
	delete(labels, pkgconfig.DevcontainerIDLabel)
	require.NoError(t, verify())
	contract.Workspace = "other"
	labels[composeContractLabel] = marshalConfigValue(t, contract)
	require.Error(t, verify())
	contract.Workspace = r.id
	labels[composeContractLabel] = marshalConfigValue(t, contract)
	r.idLabels = []string{"custom-workspace=" + r.id}
	require.Error(t, verify())
	labels["custom-workspace"] = r.id
	require.NoError(t, verify())
}

func composeMetadataFixture(
	t *testing.T,
) (*config.SubstitutedConfig, *config.ImageMetadataConfig, string) {
	t.Helper()
	dir := t.TempDir()
	baseFile := filepath.Join(dir, "compose.yaml")
	require.NoError(
		t,
		os.WriteFile(
			baseFile,
			[]byte("services:\n  app:\n    image: test:base\n    labels:\n      user: retained\n"),
			0o600,
		),
	)
	base := &config.DevContainerConfig{
		Origin: filepath.Join(dir, "devcontainer.json"),
		ComposeContainer: config.ComposeContainer{
			Service:           composeSecretTestServiceName,
			DockerComposeFile: []string{baseFile},
		},
		DevContainerActions: config.DevContainerActions{
			PostStartCommand: types.LifecycleHook{"": {"echo base"}},
		},
	}
	overlay := &config.DevContainerConfig{DevContainerConfigBase: config.DevContainerConfigBase{
		Features: map[string]any{
			"feature:private": map[string]any{"option": "private-feature-option"},
		},
		InitializeCommand: types.LifecycleHook{"": {"host-only-initialization"}},
	}, DevContainerActions: config.DevContainerActions{
		PostStartCommand: types.LifecycleHook{
			composeTestOverlayHook: {"sh", "-c", "echo $HOME ${literal} $$"},
		},
	}}
	featureEntry := &config.ImageMetadata{DevContainerActions: config.DevContainerActions{
		PostStartCommand: types.LifecycleHook{"": {"echo feature"}},
	}}
	chain := []*config.ImageMetadata{featureEntry, metadata.DevContainerConfigToImageMetadata(base)}
	parsed := &config.SubstitutedConfig{Raw: base, Config: base, Overlay: overlay}
	imageMetadata := &config.ImageMetadataConfig{Raw: chain, Config: chain}
	return parsed, imageMetadata, baseFile
}
