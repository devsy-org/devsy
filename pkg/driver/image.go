package driver

import (
	"context"
	"io"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

type ImageInspector interface {
	InspectImage(ctx context.Context, imageName string) (*config.ImageDetails, error)
	GetImageTag(ctx context.Context, imageName string) (string, error)
}

type ImageBuilder interface {
	BuildDevContainer(ctx context.Context, req BuildRequest) (*config.BuildInfo, error)
}

type ImagePublisher interface {
	PushDevContainer(ctx context.Context, image string) error
	TagDevContainer(ctx context.Context, image, tag string) error
}

// ImageBackend prepares and publishes images independently of runtime lifecycle.
type ImageBackend interface {
	ImageInspector
	ImageBuilder
	ImagePublisher
}

type ImageRunner interface {
	Driver
	RunImageDevContainer(ctx context.Context, params *RunImageDevContainerParams) error
}

type ContainerUserUpdater interface {
	UpdateContainerUserUID(
		ctx context.Context,
		workspaceID string,
		parsedConfig *config.DevContainerConfig,
		writer io.Writer,
	) error
}
