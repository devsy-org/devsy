package delivery

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/inject"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
)

type FactoryOptions struct {
	WorkspaceConfig *provider.AgentWorkspaceInfo
	WorkspaceID     string
	DockerCommand   string
	DockerEnv       []string
	HelperImage     string
	IsRemoteDocker  bool
	ContainerID     string
	ExecFunc        inject.ExecFunc //nolint:staticcheck // legacy delivery strategies require this type
	PodExec         PodExecFunc
}

func NewAgentDelivery(opts FactoryOptions) AgentDelivery {
	switch driverType := opts.WorkspaceConfig.Agent.Driver; {
	case driverType == provider.CustomDriver:
		return legacyShellDelivery(opts, "custom driver")

	case driverType == provider.KubernetesDriver:
		return kubernetesDelivery(opts)

	case driverType == provider.AppleDriver:
		// Shell delivery launches the agent in one exec, which keeps the VM
		// alive; it is the supported mechanism here, not a deprecated fallback.
		log.Debugf("using shell-based delivery for apple driver")
		return &LegacyShellDelivery{ExecFunc: opts.ExecFunc, DownloadURL: ""}

	case driverType == provider.MicrosandboxDriver:
		// Stream the agent binary over the SDK's guest exec (as kubernetes does);
		// fall back to shell delivery when the driver exposes no argv exec.
		if opts.PodExec == nil {
			return legacyShellDelivery(opts, "microsandbox argv exec unavailable")
		}
		log.Debugf("using stream delivery (exec stream) for microsandbox")
		return &KubernetesDelivery{Exec: opts.PodExec}

	case opts.IsRemoteDocker:
		log.Debugf("using remote docker delivery (docker cp)")
		return remoteDockerDelivery(opts)

	case driverType == "" || driverType == provider.DockerDriver:
		return dockerDelivery(opts)

	default:
		return legacyShellDelivery(opts, fmt.Sprintf("driver: %s", driverType))
	}
}

func kubernetesDelivery(opts FactoryOptions) AgentDelivery {
	if opts.PodExec == nil {
		return legacyShellDelivery(opts, "kubernetes pod exec unavailable")
	}
	log.Debugf("using kubernetes-native delivery (exec stream)")
	return &KubernetesDelivery{Exec: opts.PodExec}
}

func dockerDelivery(opts FactoryOptions) AgentDelivery {
	if isDockerLocal(opts.DockerCommand) {
		log.Debugf("using local docker delivery (named volume)")
		return &LocalDockerDelivery{
			DockerCommand: opts.DockerCommand,
			Environment:   opts.DockerEnv,
			HelperImage:   opts.HelperImage,
		}
	}
	log.Debugf("using remote docker delivery for non-local docker daemon")
	return remoteDockerDelivery(opts)
}

func remoteDockerDelivery(opts FactoryOptions) AgentDelivery {
	return &RemoteDockerDelivery{
		DockerCommand: opts.DockerCommand,
		Environment:   opts.DockerEnv,
		ContainerID:   opts.ContainerID,
	}
}

func legacyShellDelivery(opts FactoryOptions, reason string) AgentDelivery {
	log.Debugf("using legacy shell delivery for %s", reason)
	log.Warnf(
		"legacy shell delivery is deprecated; platform-native delivery will replace this in a future release",
	)
	return &LegacyShellDelivery{
		ExecFunc:    opts.ExecFunc,
		DownloadURL: "",
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
