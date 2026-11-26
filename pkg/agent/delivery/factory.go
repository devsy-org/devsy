package delivery

import (
	"context"
	"fmt"
	"io"

	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/inject"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
)

type FactoryOptions struct {
	IsRemoteDocker             bool
	WorkspaceID                string
	DockerCommand              string
	Runtime                    docker.RuntimeName
	HelperImage                string
	KubernetesAgentInstallPath string
	ContainerID                string
	DockerEnv                  []string
	WorkspaceConfig            *provider.AgentWorkspaceInfo
	DownloadURL                string
	ExecFunc                   inject.ExecFunc //nolint:staticcheck // legacy delivery strategies require this type
	ArgvExec                   ArgvExecFunc
	// PodExec is a compatibility alias for ArgvExec.
	PodExec PodExecFunc
}

func NewAgentDelivery(opts FactoryOptions) AgentDelivery {
	driverType := opts.WorkspaceConfig.Agent.Driver
	if opts.ArgvExec == nil {
		opts.ArgvExec = opts.PodExec
	}
	if driverType == "" || driverType == provider.DockerDriver {
		if opts.IsRemoteDocker {
			return remoteDockerDelivery(opts)
		}
		return dockerDelivery(opts)
	}
	installPath := ""
	if driverType == provider.KubernetesDriver {
		installPath = opts.KubernetesAgentInstallPath
	}
	if opts.ArgvExec != nil {
		return &KubernetesDelivery{Exec: opts.ArgvExec, InstallPath: installPath}
	}
	if driverType == provider.AppleDriver {
		return appleDelivery(opts)
	}
	return legacyShellDelivery(opts, fmt.Sprintf("driver: %s", driverType), installPath)
}

// appleDelivery launches the agent in one shell exec, which keeps the VM
// alive; it is the supported mechanism for this driver, not a deprecated
// fallback.
func appleDelivery(opts FactoryOptions) AgentDelivery {
	log.Debugf("using shell-based delivery for apple driver")
	return &LegacyShellDelivery{ExecFunc: opts.ExecFunc, DownloadURL: opts.DownloadURL}
}

// dockerDelivery is only reached when the caller (NewAgentDelivery) has
// already determined, from the workspace's resolved DOCKER_HOST, that the
// daemon is local.
func dockerDelivery(opts FactoryOptions) AgentDelivery {
	log.Debugf("using local docker delivery (named volume)")
	return &LocalDockerDelivery{
		DockerCommand: opts.DockerCommand,
		Runtime:       opts.Runtime,
		Environment:   opts.DockerEnv,
		HelperImage:   opts.HelperImage,
	}
}

// remoteDockerDelivery handles the non-local case.
func remoteDockerDelivery(opts FactoryOptions) AgentDelivery {
	return &RemoteDockerDelivery{
		DockerCommand: opts.DockerCommand,
		Environment:   opts.DockerEnv,
		ContainerID:   opts.ContainerID,
	}
}

func legacyShellDelivery(opts FactoryOptions, reason, remoteAgentPath string) AgentDelivery {
	log.Debugf("using legacy shell delivery for %s", reason)
	log.Warnf(
		"legacy shell delivery is deprecated; platform-native delivery will replace this in a future release",
	)
	return &LegacyShellDelivery{
		ExecFunc:        opts.ExecFunc,
		DownloadURL:     opts.DownloadURL,
		RemoteAgentPath: remoteAgentPath,
	}
}

// CommandFunc adapts a driver's command function to inject.ExecFunc.
func CommandFunc(
	driverCmd func(ctx context.Context, params *driver.CommandParams) error,
	workspaceID string,
) inject.ExecFunc { //nolint:staticcheck // bridges driver command signature to legacy ExecFunc
	return func(
		ctx context.Context,
		command string,
		stdin io.Reader, stdout io.Writer, stderr io.Writer,
	) error {
		return driverCmd(ctx, &driver.CommandParams{
			WorkspaceID: workspaceID,
			User:        "root",
			Command:     command,
			Stdin:       stdin,
			Stdout:      stdout,
			Stderr:      stderr,
			RawStdout:   true,
		})
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
