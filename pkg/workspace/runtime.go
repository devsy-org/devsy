package workspace

import (
	"context"
	"io"

	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
)

// ContainerRuntime abstracts container-runtime operations used for workspace
// exec and env-probe. It is runtime-agnostic: callers do not need to know
// whether Docker, Podman, or a test double is underneath.
type ContainerRuntime interface {
	// FindRunning looks up a single running container for the given workspace.
	// workspaceID may be empty when only idLabels are provided.
	FindRunning(
		ctx context.Context,
		workspaceID string,
		idLabels []string,
	) (*devcconfig.ContainerDetails, error)

	// Exec runs the command described by req inside a container. It writes
	// stdout and stderr to req.Stdout/req.Stderr and returns the process exit
	// code. A non-nil error means the exec machinery itself failed (e.g. the
	// docker binary could not be found), not that the command exited non-zero.
	Exec(ctx context.Context, req ExecRequest) (exitCode int, err error)

	// ProbeEnv queries the container's environment using the given probe mode
	// string (values defined by devcconfig.UserEnvProbe). Returns an empty map
	// on any error.
	ProbeEnv(ctx context.Context, target ContainerTarget, probeMode string) map[string]string
}

// ExecRequest bundles all parameters for a single non-interactive container
// exec. Using a struct keeps the ContainerRuntime.Exec signature within the
// revive argument-limit rule while remaining extensible.
type ExecRequest struct {
	Target  ContainerTarget
	Workdir string
	Env     map[string]string
	Argv    []string
	Stdout  io.Writer
	Stderr  io.Writer
}

// ContainerTarget identifies a single container exec target. Runtime-agnostic
// data; runtimes consume it.
type ContainerTarget struct {
	ContainerID string
	User        string
}
