package driver

import (
	"context"
	"io"

	"github.com/devsy-org/devsy/pkg/compose"
	config2 "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/provider"
)

type RunImageDevContainerParams struct {
	WorkspaceID          string
	Options              *RunOptions
	ParsedConfig         *config.DevContainerConfig
	IDE                  string
	IDEOptions           map[string]config2.OptionValue
	LocalWorkspaceFolder string
	GPUAvailability      string
}

type BuildRequest struct {
	PrebuildHash         string
	ParsedConfig         *config.SubstitutedConfig
	ExtendedBuildInfo    *feature.ExtendedBuildInfo
	DockerfilePath       string
	DockerfileContent    string
	LocalWorkspaceFolder string
	Options              provider.BuildOptions
}

// ImageDriver is a capability interface for drivers that build and run a local
// OCI image directly (e.g. Docker/Podman and Apple's `container`). It is named
// for the behavior, not a concrete runtime, since multiple runtimes implement it.
type ImageDriver interface {
	Driver

	// InspectImage inspects the given image name
	InspectImage(ctx context.Context, imageName string) (*config.ImageDetails, error)

	// GetImageTag returns latest tag for input image id
	GetImageTag(ctx context.Context, imageName string) (string, error)

	// RunImageDevContainer runs an image-based devcontainer
	RunImageDevContainer(ctx context.Context, params *RunImageDevContainerParams) error

	// BuildDevContainer builds a devcontainer
	BuildDevContainer(ctx context.Context, req BuildRequest) (*config.BuildInfo, error)

	// PushDevContainer pushes the given image to a registry
	PushDevContainer(ctx context.Context, image string) error

	// TagDevContainer tags the given image with the given tag
	TagDevContainer(ctx context.Context, image, tag string) error

	// UpdateContainerUserUID updates the container user UID/GID to match local user
	UpdateContainerUserUID(
		ctx context.Context,
		workspaceId string,
		parsedConfig *config.DevContainerConfig,
		writer io.Writer,
	) error
}

// ComposeDriver is a capability interface implemented by drivers that can run
// docker-compose based devcontainers. Not every container runtime has a compose
// engine (e.g. Apple's `container`), so callers detect support via a type
// assertion rather than forcing every driver to stub the method.
type ComposeDriver interface {
	Driver

	// ComposeHelper returns the compose helper
	ComposeHelper() (*compose.ComposeHelper, error)
}

// DockerHelperProvider is a capability interface implemented by drivers backed
// by a Docker-compatible CLI that can expose the low-level *docker.DockerHelper.
// Runtimes without one (e.g. the Apple driver) simply do not implement it.
type DockerHelperProvider interface {
	Driver

	// DockerHelper returns the docker helper
	DockerHelper() (*docker.DockerHelper, error)
}

// SnapshotCapableDriver is a capability interface implemented by drivers that
// can commit a running container's filesystem to a new image. Not every
// ImageDriver can do this (e.g. Apple's `container`), so callers detect
// support via a type assertion rather than forcing every ImageDriver to stub
// the method — the same pattern ComposeDriver and DockerHelperProvider
// already establish in this file.
type SnapshotCapableDriver interface {
	Driver

	// CommitContainer commits the running devcontainer's filesystem to a new
	// image, tagged as tag, to capture apt installs, global packages, and
	// other filesystem drift for workspace snapshots.
	CommitContainer(ctx context.Context, workspaceID, tag string) error

	// RemoveImage removes the given locally-tagged image, e.g. to reclaim
	// disk after a committed snapshot image has been pushed to a registry.
	RemoveImage(ctx context.Context, tag string) error
}
