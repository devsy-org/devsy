package delivery

import (
	"context"
	"fmt"
	"io"

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
	driverType := opts.WorkspaceConfig.Agent.Driver
	if d := namedDriverDelivery(driverType, opts); d != nil {
		return d
	}

	if opts.IsRemoteDocker {
		log.Debugf("using remote docker delivery (docker cp)")
		return remoteDockerDelivery(opts)
	}

	if driverType == "" || driverType == provider.DockerDriver {
		return dockerDelivery(opts)
	}

	return legacyShellDelivery(opts, fmt.Sprintf("driver: %s", driverType))
}

// namedDriverDelivery returns the delivery strategy for driver types that
// dispatch on an exact name match, or nil if driverType matches none of them.
func namedDriverDelivery(driverType string, opts FactoryOptions) AgentDelivery {
	switch driverType {
	case provider.CustomDriver:
		return legacyShellDelivery(opts, "custom driver")
	case provider.KubernetesDriver:
		return kubernetesDelivery(opts)
	case provider.AppleDriver:
		return appleDelivery(opts)
	case provider.MicrosandboxDriver:
		return microsandboxDelivery(opts)
	default:
		return nil
	}
}

// appleDelivery launches the agent in one shell exec, which keeps the VM
// alive; it is the supported mechanism for this driver, not a deprecated
// fallback.
func appleDelivery(opts FactoryOptions) AgentDelivery {
	log.Debugf("using shell-based delivery for apple driver")
	return &LegacyShellDelivery{ExecFunc: opts.ExecFunc, DownloadURL: ""}
}

func kubernetesDelivery(opts FactoryOptions) AgentDelivery {
	if opts.PodExec == nil {
		return legacyShellDelivery(opts, "kubernetes pod exec unavailable")
	}
	log.Debugf("using kubernetes-native delivery (exec stream)")
	return &KubernetesDelivery{Exec: opts.PodExec}
}

// microsandboxDelivery streams the agent binary over the SDK's guest exec
// (as kubernetes does), falling back to shell delivery when the driver
// exposes no argv exec.
func microsandboxDelivery(opts FactoryOptions) AgentDelivery {
	if opts.PodExec == nil {
		return legacyShellDelivery(opts, "microsandbox argv exec unavailable")
	}
	log.Debugf("using stream delivery (exec stream) for microsandbox")
	return &KubernetesDelivery{Exec: opts.PodExec}
}

// dockerDelivery is only reached when the caller (NewAgentDelivery) has
// already determined, from the workspace's resolved DOCKER_HOST, that the
// daemon is local.
func dockerDelivery(opts FactoryOptions) AgentDelivery {
	log.Debugf("using local docker delivery (named volume)")
	return &LocalDockerDelivery{
		DockerCommand: opts.DockerCommand,
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
