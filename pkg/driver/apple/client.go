package apple

import (
	"context"
	"io"

	"github.com/devsy-org/devsy/pkg/apple"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

// appleClient is the subset of *apple.AppleHelper the driver depends on, so its
// orchestration can be unit-tested with a mock instead of the real CLI.
type appleClient interface {
	EnsureBuilderRunning(ctx context.Context) error
	FindDevContainer(ctx context.Context, labels []string) (*config.ContainerDetails, error)
	FindContainerByID(ctx context.Context, ids []string) (*config.ContainerDetails, error)
	InspectImage(
		ctx context.Context,
		imageName string,
		tryRemote bool,
	) (*config.ImageDetails, error)
	GetImageTag(ctx context.Context, imageID string) (string, error)
	Pull(ctx context.Context, opts apple.PullOptions) error
	Push(ctx context.Context, image string, stdout, stderr io.Writer) error
	Tag(ctx context.Context, image, tag string) error
	Run(ctx context.Context, args []string, s apple.Streams) error
	RunWithDir(ctx context.Context, dir string, args []string, s apple.Streams) error
	StartContainer(ctx context.Context, id string) error
	WaitContainerRunning(ctx context.Context, id string) error
	Stop(ctx context.Context, id string) error
	Remove(ctx context.Context, id string) error
	GetContainerLogs(ctx context.Context, id string, stdout, stderr io.Writer) error
}

var _ appleClient = (*apple.AppleHelper)(nil)
