package delivery

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/version"
)

var _ AgentDelivery = (*KubernetesDelivery)(nil)

// PodExecFunc runs argv in the workspace pod's dev container with the given streams.
type PodExecFunc func(ctx context.Context, argv []string, streams driver.Streams) error

// KubernetesDelivery streams the agent binary into the pod over the cluster's exec API.
type KubernetesDelivery struct {
	Exec PodExecFunc

	// ExpectedVersion defaults to version.GetVersion() when empty.
	ExpectedVersion string
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
	if d.Exec == nil {
		return fmt.Errorf("exec function is required for kubernetes delivery")
	}

	destPath := pkgconfig.ContainerDevsyHelperLocation

	// Skip delivery when the in-pod binary already matches.
	expected := d.expectedVersion()
	if actual := d.detectVersion(ctx, destPath); actual != "" && actual == expected {
		log.Debugf("remote agent version matches expected version %s, skipping delivery", expected)
		return nil
	}

	binary, err := opts.BinarySource(ctx, opts.Arch)
	if err != nil {
		return fmt.Errorf("acquire binary: %w", err)
	}
	defer func() { _ = binary.Close() }()

	// Write to a temp file and atomically move it into place so a failed stream
	// never leaves an executable stub.
	script := fmt.Sprintf(
		`set -e; d=$(dirname %s); mkdir -p "$d"; `+
			`t=$(mktemp %s.XXXXXX); `+
			`cat > "$t" && chmod 0755 "$t" && mv -f "$t" %s || { rm -f "$t"; exit 1; }`,
		destPath, destPath, destPath,
	)

	if err := d.Exec(ctx, []string{"sh", "-c", script}, driver.Streams{Stdin: binary}); err != nil {
		return fmt.Errorf("write binary to container: %w", err)
	}

	log.Debugf("delivered agent binary to pod via kubernetes exec")
	return nil
}

func (d *KubernetesDelivery) Cleanup(_ context.Context, _ string) error {
	return nil
}

func (d *KubernetesDelivery) expectedVersion() string {
	if d.ExpectedVersion != "" {
		return d.ExpectedVersion
	}
	return version.GetVersion()
}

// detectVersion returns the agent version in the pod, or "" if absent or unprobeable.
func (d *KubernetesDelivery) detectVersion(ctx context.Context, destPath string) string {
	script := fmt.Sprintf(`[ -x "%s" ] && "%s" --version 2>/dev/null || true`, destPath, destPath)

	var stdout bytes.Buffer
	err := d.Exec(ctx, []string{"sh", "-c", script}, driver.Streams{Stdout: &stdout})
	if err != nil {
		log.Debugf("failed to detect agent version in pod: %v", err)
		return ""
	}
	return strings.TrimSpace(stdout.String())
}
