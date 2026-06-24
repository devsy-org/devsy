package driver

import (
	"context"
	"io"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

// Driver is the default interface for Devsy drivers.
type Driver interface {
	// FindDevContainer returns a running devcontainer details
	FindDevContainer(ctx context.Context, workspaceID string) (*config.ContainerDetails, error)

	// CommandDevContainer runs the given command inside the devcontainer
	CommandDevContainer(ctx context.Context, params *CommandParams) error

	// RunDevContainer runs a devcontainer
	RunDevContainer(ctx context.Context, workspaceID string, options *RunOptions) error

	// TargetArchitecture returns the architecture of the container runtime. e.g. amd64 or arm64
	TargetArchitecture(ctx context.Context, workspaceID string) (string, error)

	// DeleteDevContainer deletes the devcontainer
	DeleteDevContainer(ctx context.Context, workspaceID string) error

	// StartDevContainer starts the devcontainer
	StartDevContainer(ctx context.Context, workspaceID string) error

	// StopDevContainer stops the devcontainer
	StopDevContainer(ctx context.Context, workspaceID string) error

	// GetContainerLogs returns the logs of the devcontainer
	GetDevContainerLogs(
		ctx context.Context,
		workspaceID string,
		stdout io.Writer,
		stderr io.Writer,
	) error
}

type ReprovisioningDriver interface {
	Driver

	// CanReprovision returns true if the driver can reprovision the devcontainer
	CanReprovision() bool
}

// CommandParams holds the parameters for running a command inside a devcontainer.
type CommandParams struct {
	WorkspaceID string
	User        string
	Command     string
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
}

// Streams bundles the standard IO streams for an exec.
type Streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// RunOptions are the options for running a container.
type RunOptions struct {
	// UID is a unique identifier for this workspace
	UID string `json:"uid,omitempty"`

	// Image is the image to run
	Image string `json:"image,omitempty"`

	// User is the user to run the container as
	User string `json:"user,omitempty"`

	// Entrypoint is the entrypoint of the container
	Entrypoint string `json:"entrypoint,omitempty"`

	// Cmd are the cmd for the entrypoint
	Cmd []string `json:"cmd,omitempty"`

	// Env are additional environment variables to set
	Env map[string]string `json:"env,omitempty"`

	// CapAdd are additional capabilities for the container
	CapAdd []string `json:"capAdd,omitempty"`

	// SecurityOpt are additional security options
	SecurityOpt []string `json:"securityOpt,omitempty"`

	// Labels are labels to set on the container
	Labels []string `json:"labels,omitempty"`

	// Privileged indicates if the container should run with elevated permissions
	Privileged *bool `json:"privileged,omitempty"`

	// Init passes the --init flag when creating the container
	Init *bool `json:"init,omitempty"`

	// WorkspaceMount is the mount where the workspace should get mounted
	WorkspaceMount *config.Mount `json:"workspaceMount,omitempty"`

	// Mounts are additional mounts on the container. Supported are volume and bind mounts.
	// Bind mounts are expected to get copied from local to remote once. Volume mounts are expected
	// to be persisted for the lifetime of the container.
	Mounts []*config.Mount `json:"mounts,omitempty"`

	// Userns is the user namespace to use for the container
	Userns string `json:"userns,omitempty"`

	// UidMap are UID mappings for user namespace
	UidMap []string `json:"uidMap,omitempty"`

	// GidMap are GID mappings for user namespace
	GidMap []string `json:"gidMap,omitempty"`

	// Platform is the target platform (os/arch) to run the container under,
	// e.g. "linux/amd64". Empty means use the host's native platform.
	Platform string `json:"platform,omitempty"`
}
