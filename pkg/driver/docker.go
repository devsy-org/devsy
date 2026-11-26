package driver

import (
	"context"

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

// ImageDriver is the legacy aggregate of runtime and image capabilities.
// New callers should use the individual capabilities or an ImageBackend.
type ImageDriver interface {
	ImageRunner
	ImageBackend
	ContainerUserUpdater
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
// the method - the same pattern ComposeDriver and DockerHelperProvider
// already establish in this file.
type SnapshotCapableDriver interface {
	Driver

	// CommitContainer commits the running devcontainer's filesystem to a new
	// image, tagged as tag, to capture apt installs, global packages, and
	// other filesystem drift for workspace snapshots.
	CommitContainer(ctx context.Context, workspaceID, tag string) error
}
