package delivery

import (
	"context"
	"fmt"
	"io"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/inject"
	"github.com/devsy-org/devsy/pkg/log"
)

// Deprecated: LegacyShellDelivery is deprecated. Platform-native AgentDelivery implementations
// (LocalDockerDelivery, RemoteDockerDelivery, KubernetesDelivery) are the replacements.
type LegacyShellDelivery struct {
	ExecFunc    inject.ExecFunc
	DownloadURL string
	Timeout     func() *agent.InjectOptions
}

// Deprecated: Phase is part of LegacyShellDelivery which is deprecated.
func (d *LegacyShellDelivery) Phase() DeliveryPhase {
	return PhasePostStart
}

// Deprecated: DeliverPreStart is part of LegacyShellDelivery which is deprecated.
func (d *LegacyShellDelivery) DeliverPreStart(_ context.Context, _ PreStartOptions) error {
	return fmt.Errorf("LegacyShellDelivery does not support pre-start delivery")
}

// Deprecated: DeliverPostStart is part of LegacyShellDelivery which is deprecated.
func (d *LegacyShellDelivery) DeliverPostStart(ctx context.Context, opts PostStartOptions) error {
	if d.ExecFunc == nil {
		return fmt.Errorf("exec function is required for legacy shell delivery")
	}

	injectOpts := &agent.InjectOptions{
		Ctx:                         ctx,
		Exec:                        d.ExecFunc,
		IsLocal:                     false,
		RemoteAgentPath:             agent.ContainerDevsyHelperLocation,
		DownloadURL:                 d.downloadURL(),
		PreferDownloadFromRemoteUrl: agent.Bool(false),
	}

	if d.Timeout != nil {
		overrides := d.Timeout()
		if overrides != nil && overrides.Timeout > 0 {
			injectOpts.Timeout = overrides.Timeout
		}
	}

	if err := agent.InjectAgent(injectOpts); err != nil {
		return fmt.Errorf("legacy shell inject: %w", err)
	}

	log.Debugf("delivered agent binary via legacy shell injection")
	return nil
}

// Deprecated: Cleanup is part of LegacyShellDelivery which is deprecated.
func (d *LegacyShellDelivery) Cleanup(_ context.Context, _ string) error {
	return nil
}

func (d *LegacyShellDelivery) downloadURL() string {
	if d.DownloadURL != "" {
		return d.DownloadURL
	}
	return agent.DefaultAgentDownloadURL()
}

// ExecFuncFromDriver creates an inject.ExecFunc that routes commands through
// the provided driver command function. This adapts the driver's
// CommandDevContainer signature for use with the legacy injection path.
//
// Deprecated: Platform-native AgentDelivery implementations
// (LocalDockerDelivery, RemoteDockerDelivery, KubernetesDelivery) are the replacements.
func ExecFuncFromDriver(
	cmdFn func(ctx context.Context, user, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error,
	user string,
) inject.ExecFunc {
	return func(ctx context.Context, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
		return cmdFn(ctx, user, command, stdin, stdout, stderr)
	}
}
