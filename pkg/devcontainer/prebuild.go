package devcontainer

import (
	"context"
	"fmt"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/build"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/devsy-org/devsy/pkg/provider"
)

func (r *runner) Build(ctx context.Context, options provider.BuildOptions) (string, error) {
	dockerDriver, ok := r.driver.(driver.ImageDriver)
	if !ok {
		return "", fmt.Errorf("building only supported with docker driver")
	}

	substitutedConfig, substitutionContext, err := r.getSubstitutedConfig(options.CLIOptions)
	if err != nil {
		return "", err
	}

	prebuildRepo := getPrebuildRepository(substitutedConfig)

	defer config.RemoveBuildArtifacts(config.GetContextPath(substitutedConfig.Config))

	// check if we need to build container
	buildInfo, err := r.build(ctx, substitutedConfig, substitutionContext, options)
	if err != nil {
		return "", fmt.Errorf("build image: %w", err)
	}

	prebuildImage := r.determinePrebuildImage(options, prebuildRepo, buildInfo)

	if buildInfo.ImageName == prebuildImage {
		return buildInfo.ImageName, nil
	}

	// should we push?
	if options.SkipPush {
		return prebuildImage, nil
	}

	// if push happened during build, skip the separate push workflow
	// When PushDuringBuild is enabled, BuildKit already pushed the image directly to the
	// registry during the build process. Skip the tag/push workflow below to avoid
	// redundant operations. The image is already in the registry.
	if options.PushDuringBuild {
		return prebuildImage, nil
	}

	if err := r.pushPrebuildImage(ctx, pushPrebuildImageParams{
		dockerDriver:      dockerDriver,
		substitutedConfig: substitutedConfig,
		buildInfo:         buildInfo,
		prebuildImage:     prebuildImage,
		options:           options,
		prebuildRepo:      prebuildRepo,
	}); err != nil {
		return "", err
	}

	return prebuildImage, nil
}

// determinePrebuildImage computes the prebuild image reference, defaulting an
// empty PrebuildHash to "latest" to avoid an invalid reference format on push.
func (r *runner) determinePrebuildImage(
	options provider.BuildOptions,
	prebuildRepo string,
	buildInfo *config.BuildInfo,
) string {
	if buildInfo.PrebuildHash == "" {
		buildInfo.PrebuildHash = "latest"
	}

	switch {
	case options.Repository != "":
		return options.Repository + ":" + buildInfo.PrebuildHash
	case prebuildRepo != "":
		return prebuildRepo + ":" + buildInfo.PrebuildHash
	default:
		return build.GetImageName(r.localWorkspaceFolder, buildInfo.PrebuildHash)
	}
}

type pushPrebuildImageParams struct {
	dockerDriver      driver.ImageDriver
	substitutedConfig *config.SubstitutedConfig
	buildInfo         *config.BuildInfo
	prebuildImage     string
	options           provider.BuildOptions
	prebuildRepo      string
}

func (r *runner) pushPrebuildImage(ctx context.Context, params pushPrebuildImageParams) error {
	if isDockerComposeConfig(params.substitutedConfig.Config) {
		if err := params.dockerDriver.TagDevContainer(
			ctx,
			params.buildInfo.ImageName,
			params.prebuildImage,
		); err != nil {
			return fmt.Errorf("tag image: %w", err)
		}
	}

	// if no repository is specified, skip push (mirrors devcontainer CLI behavior)
	if params.options.Repository == "" && params.prebuildRepo == "" {
		return nil
	}

	// check if we can push image
	if err := image.CheckPushPermissions(ctx, params.prebuildImage); err != nil {
		return fmt.Errorf(
			"cannot push to repository %s. Make sure you are logged into the registry "+
				"and credentials are available. (Error: %w)",
			params.prebuildImage,
			err,
		)
	}

	return tagAndPushImages(ctx, params.dockerDriver, params.prebuildImage, params.buildInfo.Tags)
}

func tagAndPushImages(
	ctx context.Context,
	dockerDriver driver.ImageDriver,
	prebuildImage string,
	tags []string,
) error {
	// Setup all image tags (prebuild and any user defined tags)
	imageRefs := []string{prebuildImage}

	imageRepoName := stripImageTag(prebuildImage)
	for _, tag := range tags {
		imageRefs = append(imageRefs, imageRepoName+":"+tag)
	}

	// tag the image
	for _, imageRef := range imageRefs {
		if err := dockerDriver.TagDevContainer(ctx, prebuildImage, imageRef); err != nil {
			return fmt.Errorf("tag image: %w", err)
		}
	}

	// push the image to the registry
	for _, imageRef := range imageRefs {
		if err := dockerDriver.PushDevContainer(ctx, imageRef); err != nil {
			return fmt.Errorf("push image: %w", err)
		}
	}

	return nil
}

// stripImageTag returns the image reference without its trailing tag suffix,
// preserving any registry port (host:port) and repository path. A tag
// separator only counts when it appears after the last path separator.
func stripImageTag(image string) string {
	lastSlash := strings.LastIndex(image, "/")
	if colon := strings.LastIndex(image, ":"); colon > lastSlash {
		return image[:colon]
	}
	return image
}

func getPrebuildRepository(substitutedConfig *config.SubstitutedConfig) string {
	if len(config.GetDevsyCustomizations(substitutedConfig.Config).PrebuildRepository) > 0 {
		return config.GetDevsyCustomizations(substitutedConfig.Config).PrebuildRepository[0]
	}

	return ""
}
