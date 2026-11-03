package devcontainer

import (
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
)

func (r *runner) prepareImageDetails(
	parsed *config.SubstitutedConfig,
	options UpOptions,
	defaultSelection bool,
) error {
	r.imageDetails = nil
	r.imageDetailsImage = ""
	d, ok := r.driver.(driver.ImageDetailsDriver)
	if !ok {
		return nil
	}
	c := parsed.Config
	reason := r.imageDetailsConfigReason(c, options, defaultSelection)
	if reason == "" {
		reason = imageDetailsOperationReason(c, options)
	}
	details, err := d.ImageDetails(
		driver.ImageDetailsRequest{ImageName: c.Image, UnsupportedReason: reason},
	)
	if err != nil {
		return err
	}
	r.imageDetails = details
	r.imageDetailsImage = c.Image
	return nil
}

func (r *runner) imageDetailsConfigReason(
	c *config.DevContainerConfig,
	options UpOptions,
	defaultSelection bool,
) string {
	switch {
	case isDockerFileConfig(c) || c.Build != nil:
		return "Dockerfile or build configuration"
	case isDockerComposeConfig(c):
		return "Docker Compose"
	default:
		return r.imageDetailsContainerReason(c, options, defaultSelection)
	}
}

func (r *runner) imageDetailsContainerReason(
	c *config.DevContainerConfig,
	options UpOptions,
	defaultSelection bool,
) string {
	switch {
	case c.ContainerID != "" || r.imageDetailsAttachedSource():
		return "attached existing container"
	case defaultSelection || r.imageDetailsImplicitSource(options) || c.Image == "":
		return "an explicitly selected image is required"
	case len(c.Features) != 0:
		return "features"
	default:
		return ""
	}
}

func (r *runner) imageDetailsAttachedSource() bool {
	return r.workspaceConfig != nil && r.workspaceConfig.Workspace != nil &&
		r.workspaceConfig.Workspace.Source.Container != ""
}

func (r *runner) imageDetailsImplicitSource(options UpOptions) bool {
	selection := r.effectiveDevContainerSelection(options.CLIOptions)
	if selection.source == "" {
		return false
	}
	source, err := ParseSourceSpec(selection.source)
	return err == nil && source.Kind == SourceNone && options.DevContainerImage == ""
}

func imageDetailsOperationReason(c *config.DevContainerConfig, options UpOptions) string {
	switch {
	case options.ForceDockerless:
		return "forced dockerless"
	case options.Recovery:
		return "recovery mode"
	case len(options.PrebuildRepositories) != 0 ||
		len(config.GetDevsyCustomizations(c).PrebuildRepository) != 0:
		return "prebuild repository lookup"
	case imageDetailsBuildRequested(options):
		return "build, push or export options"
	default:
		return ""
	}
}

func imageDetailsBuildRequested(options UpOptions) bool {
	return imageDetailsBuildOutputRequested(options) ||
		imageDetailsBuildTagRequested(options) ||
		imageDetailsBuildExecutionRequested(options) ||
		imageDetailsBuildInputsRequested(options) ||
		imageDetailsBuildLockfileRequested(options)
}

func imageDetailsBuildOutputRequested(options UpOptions) bool {
	return options.Prebuild || options.ForceBuild || options.Repository != "" ||
		options.SkipPush || options.PushDuringBuild || options.Output != ""
}

func imageDetailsBuildTagRequested(options UpOptions) bool {
	return len(options.Platforms) != 0 || len(options.Tag) != 0 || options.ImageName != ""
}

func imageDetailsBuildExecutionRequested(options UpOptions) bool {
	return options.ForceInternalBuildKit || options.RegistryCache != "" ||
		options.NoCache || options.Pull || len(options.CacheFrom) != 0
}

func imageDetailsBuildInputsRequested(options UpOptions) bool {
	return len(options.BuildSecrets) != 0 || len(options.BuildSecretNames) != 0 ||
		len(options.Labels) != 0
}

func imageDetailsBuildLockfileRequested(options UpOptions) bool {
	return options.ExperimentalLockfile != "" || options.FrozenLockfile || options.NoLockfile
}
