package delivery

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/devsy-org/devsy/pkg/inject"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
)

type FactoryOptions struct {
	WorkspaceConfig *provider.AgentWorkspaceInfo
	WorkspaceID     string
	DockerCommand   string
	DockerEnv       []string
	IsRemoteDocker  bool
	ContainerID     string
	ExecFunc        inject.ExecFunc
}

func NewAgentDelivery(opts FactoryOptions) AgentDelivery {
	driverType := opts.WorkspaceConfig.Agent.Driver

	switch {
	case driverType == provider.CustomDriver:
		log.Debugf("using legacy shell delivery for custom driver")
		return &LegacyShellDelivery{
			ExecFunc:    opts.ExecFunc,
			DownloadURL: "",
		}

	case opts.IsRemoteDocker:
		log.Debugf("using remote docker delivery (docker cp)")
		return &RemoteDockerDelivery{
			DockerCommand: opts.DockerCommand,
			Environment:   opts.DockerEnv,
			ContainerID:   opts.ContainerID,
		}

	case driverType == "" || driverType == provider.DockerDriver:
		if isDockerLocal(opts.DockerCommand) {
			log.Debugf("using local docker delivery (named volume)")
			return &LocalDockerDelivery{
				DockerCommand: opts.DockerCommand,
				Environment:   opts.DockerEnv,
			}
		}
		log.Debugf("using remote docker delivery for non-local docker daemon")
		return &RemoteDockerDelivery{
			DockerCommand: opts.DockerCommand,
			Environment:   opts.DockerEnv,
			ContainerID:   opts.ContainerID,
		}

	default:
		log.Debugf("using legacy shell delivery for driver: %s", driverType)
		return &LegacyShellDelivery{
			ExecFunc:    opts.ExecFunc,
			DownloadURL: "",
		}
	}
}

func isDockerLocal(_ string) bool {
	envHost := os.Getenv("DOCKER_HOST")
	return envHost == "" || isLocalDockerHost(envHost)
}

func isLocalDockerHost(host string) bool {
	if host == "" {
		return true
	}
	hasPrefix := func(s, prefix string) bool {
		return len(s) >= len(prefix) && s[:len(prefix)] == prefix
	}
	return hasPrefix(host, "unix://") || hasPrefix(host, "npipe://")
}

// CommandFunc adapts a driver's command function to inject.ExecFunc.
func CommandFunc(
	driverCmd func(
		ctx context.Context,
		workspaceID, user, command string,
		stdin io.Reader, stdout io.Writer, stderr io.Writer,
	) error,
	workspaceID string,
) inject.ExecFunc {
	return func(
		ctx context.Context,
		command string,
		stdin io.Reader, stdout io.Writer, stderr io.Writer,
	) error {
		return driverCmd(ctx, workspaceID, "root", command, stdin, stdout, stderr)
	}
}

// Deliver calls the appropriate delivery method based on the strategy's phase.
func Deliver(
	ctx context.Context,
	strategy AgentDelivery,
	preOpts *PreStartOptions,
	postOpts *PostStartOptions,
) error {
	switch strategy.Phase() {
	case PhasePreStart:
		if preOpts == nil {
			return fmt.Errorf(
				"pre-start options required for %s delivery", strategy.Phase(),
			)
		}
		return strategy.DeliverPreStart(ctx, *preOpts)
	case PhasePostStart:
		if postOpts == nil {
			return fmt.Errorf(
				"post-start options required for %s delivery", strategy.Phase(),
			)
		}
		return strategy.DeliverPostStart(ctx, *postOpts)
	default:
		return fmt.Errorf("unknown delivery phase: %s", strategy.Phase())
	}
}
