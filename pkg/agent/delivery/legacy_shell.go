package delivery

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/devsy-org/devsy/pkg/agent"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/inject"
	"github.com/devsy-org/devsy/pkg/log"
)

type LegacyShellDelivery struct {
	ExecFunc    inject.ExecFunc //nolint:staticcheck
	DownloadURL string
	Timeout     func() time.Duration
}

func (d *LegacyShellDelivery) Phase() DeliveryPhase {
	return PhasePostStart
}

func (d *LegacyShellDelivery) DeliverPreStart(_ context.Context, _ PreStartOptions) error {
	return fmt.Errorf("LegacyShellDelivery does not support pre-start delivery")
}

func (d *LegacyShellDelivery) DeliverPostStart(ctx context.Context, opts PostStartOptions) error {
	if d.ExecFunc == nil {
		return fmt.Errorf("exec function is required for legacy shell delivery")
	}

	if err := agent.InjectAgent(ctx, &agent.InjectOptions{
		Exec:                        d.ExecFunc,
		IsLocal:                     false,
		RemoteAgentPath:             pkgconfig.ContainerDevsyHelperLocation,
		DownloadURL:                 d.downloadURL(),
		PreferDownloadFromRemoteUrl: new(false),
		Timeout:                     d.timeout(),
	}); err != nil {
		return fmt.Errorf("legacy shell inject: %w", err)
	}

	log.Debugf("delivered agent binary via legacy shell injection")
	return nil
}

func (d *LegacyShellDelivery) Cleanup(_ context.Context, _ string) error {
	return nil
}

func (d *LegacyShellDelivery) timeout() time.Duration {
	if d.Timeout == nil {
		return 0
	}
	return d.Timeout()
}

func (d *LegacyShellDelivery) downloadURL() string {
	if d.DownloadURL != "" {
		return d.DownloadURL
	}
	return pkgconfig.DefaultAgentDownloadURL()
}

func ExecFuncFromDriver(
	cmdFn func(ctx context.Context, user, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error,
	user string,
) inject.ExecFunc {
	return func(ctx context.Context, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
		return cmdFn(ctx, user, command, stdin, stdout, stderr)
	}
}
