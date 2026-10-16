package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/containerd/platforms"
	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	imagetypes "github.com/google/go-containerregistry/pkg/v1/types"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// InspectImageForPlatform returns the selected platform's configuration while
// retaining the daemon-addressable image ID. With a multi-platform store, the
// selected manifest alone is not necessarily addressable as a local image.
func (r *DockerHelper) InspectImageForPlatform(
	ctx context.Context,
	image, platform string,
) (*config.ImageDetails, error) {
	base, err := r.InspectImage(ctx, image, false)
	if err != nil {
		return nil, err
	}
	if base == nil {
		return nil, ErrImageNotFound
	}
	platform, inspectVariant, err := imageInspectionPlatform(base, platform)
	if err != nil {
		return nil, err
	}
	if !inspectVariant {
		return base, nil
	}
	selected, err := r.inspectPlatformOrFallback(ctx, image, platform, base)
	if err != nil {
		return nil, err
	}
	if err := validateImagePlatform(image, platform, selected); err != nil {
		return nil, err
	}
	return resolvedPlatformImage(base, selected), nil
}

func imageInspectionPlatform(details *config.ImageDetails, requested string) (string, bool, error) {
	if requested != "" {
		return requested, true, nil
	}
	if !isImageIndex(details.Descriptor) {
		return "", false, nil
	}
	if details.OS == "" || details.Architecture == "" {
		return "", false, ErrImageNotFound
	}
	return platforms.Format(ocispec.Platform{
		OS: details.OS, Architecture: details.Architecture, Variant: details.Variant,
	}), true, nil
}

func (r *DockerHelper) inspectPlatformOrFallback(
	ctx context.Context,
	image, platform string,
	base *config.ImageDetails,
) (*config.ImageDetails, error) {
	out, err := r.buildCmd(ctx, "image", "inspect", "--platform", platform, image).Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("inspect image platform: %w", ctx.Err())
		}
		wrapped := command.WrapCommandError(nil, err)
		message := strings.ToLower(wrapped.Error())
		if platformInspectionUnsupported(message) {
			// Older engines and compatible runtimes have a single-platform local
			// image store. The caller still verifies that cached configuration.
			return base, nil
		}
		if platformInspectionMiss(message) {
			return nil, fmt.Errorf("%s: %w", image, ErrImageNotFound)
		}
		return nil, classifyImageError(image, fmt.Errorf("inspect image platform: %w", wrapped))
	}
	var details []*config.ImageDetails
	if err := json.Unmarshal(out, &details); err != nil {
		return nil, fmt.Errorf("parse platform image inspection: %w", err)
	}
	if len(details) == 0 || details[0] == nil {
		return nil, ErrImageNotFound
	}
	return details[0], nil
}

func platformInspectionUnsupported(message string) bool {
	return strings.Contains(message, "unknown flag: --platform") ||
		(strings.Contains(message, "--platform") && strings.Contains(message, "requires api version"))
}

func platformInspectionMiss(message string) bool {
	return strings.Contains(message, "does not match the specified platform") ||
		strings.Contains(message, "no matching manifest for") ||
		strings.Contains(message, "no image found for platform")
}

func validateImagePlatform(image, platform string, details *config.ImageDetails) error {
	requested, err := platforms.Parse(platform)
	if err != nil {
		return fmt.Errorf("parse image platform: %w", err)
	}
	actual := ocispec.Platform{
		OS: details.OS, Architecture: details.Architecture, Variant: details.Variant,
	}
	if !platforms.OnlyStrict(platforms.Normalize(requested)).Match(platforms.Normalize(actual)) {
		return fmt.Errorf(
			"image %q has no cached variant for platform %q: %w",
			image,
			platform,
			ErrImageNotFound,
		)
	}
	return nil
}

func isImageIndex(descriptor *ocispec.Descriptor) bool {
	return descriptor != nil && (descriptor.MediaType == string(imagetypes.OCIImageIndex) ||
		descriptor.MediaType == string(imagetypes.DockerManifestList))
}

func resolvedPlatformImage(base, selected *config.ImageDetails) *config.ImageDetails {
	resolved := *selected
	resolved.ID = base.ID
	resolved.Metadata = base.Metadata
	resolved.Descriptor = base.Descriptor
	if isImageIndex(base.Descriptor) && selected.Descriptor != nil &&
		!isImageIndex(selected.Descriptor) {
		resolved.PlatformManifest = string(selected.Descriptor.Digest)
	}
	return &resolved
}
