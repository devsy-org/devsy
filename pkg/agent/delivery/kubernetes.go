package delivery

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/inject"
	"github.com/devsy-org/devsy/pkg/log"
)

var _ AgentDelivery = (*KubernetesDelivery)(nil)

type KubernetesDelivery struct {
	ExecFunc inject.ExecFunc
}

func (d *KubernetesDelivery) Phase() DeliveryPhase {
	return PhasePostStart
}

func (d *KubernetesDelivery) DeliverPreStart(_ context.Context, _ PreStartOptions) error {
	return fmt.Errorf("KubernetesDelivery does not support pre-start delivery")
}

func (d *KubernetesDelivery) DeliverPostStart(ctx context.Context, opts PostStartOptions) error {
	if opts.BinarySource == nil {
		return fmt.Errorf("binary source is required for kubernetes delivery")
	}
	if d.ExecFunc == nil {
		return fmt.Errorf("exec function is required for kubernetes delivery")
	}

	binary, err := opts.BinarySource(ctx, opts.Arch)
	if err != nil {
		return fmt.Errorf("acquire binary: %w", err)
	}
	defer func() { _ = binary.Close() }()

	destPath := agent.ContainerDevsyHelperLocation
	script := fmt.Sprintf("cat > %s && chmod 755 %s", destPath, destPath)

	if err := d.ExecFunc(ctx, script, binary, nil, nil); err != nil {
		return fmt.Errorf("write binary to container: %w", err)
	}

	log.Debugf("delivered agent binary to kubernetes container via exec")
	return nil
}

func (d *KubernetesDelivery) Cleanup(_ context.Context, _ string) error {
	return nil
}
