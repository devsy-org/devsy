package delivery

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/log"
)

var _ AgentDelivery = (*RemoteDockerDelivery)(nil)

type RemoteDockerDelivery struct {
	DockerCommand string
	Environment   []string
	ContainerID   string
}

func (d *RemoteDockerDelivery) Phase() DeliveryPhase {
	return PhasePostStart
}

func (d *RemoteDockerDelivery) DeliverPreStart(_ context.Context, _ PreStartOptions) error {
	return fmt.Errorf("RemoteDockerDelivery does not support pre-start delivery")
}

func (d *RemoteDockerDelivery) DeliverPostStart(ctx context.Context, opts PostStartOptions) error {
	if opts.BinarySource == nil {
		return fmt.Errorf("binary source is required for remote docker delivery")
	}
	if d.ContainerID == "" && opts.ContainerDetails != nil {
		d.ContainerID = opts.ContainerDetails.ID
	}
	if d.ContainerID == "" {
		return fmt.Errorf("container ID is required for remote docker delivery")
	}

	destPath := agent.ContainerDevsyHelperLocation

	if err := d.ensureDir(ctx, destPath); err != nil {
		return fmt.Errorf("ensure target directory: %w", err)
	}

	if err := d.copyBinaryFromSource(ctx, opts.BinarySource, opts.Arch, destPath); err != nil {
		return fmt.Errorf("copy binary to container: %w", err)
	}

	if err := d.chmodBinary(ctx, destPath); err != nil {
		return fmt.Errorf("chmod binary in container: %w", err)
	}

	log.Debugf("delivered agent binary to remote container %s via docker cp", d.ContainerID)
	return nil
}

func (d *RemoteDockerDelivery) Cleanup(_ context.Context, _ string) error {
	return nil
}

func (d *RemoteDockerDelivery) copyBinaryFromSource(
	ctx context.Context,
	binarySource BinarySourceFunc,
	arch, destPath string,
) error {
	binary, err := binarySource(ctx, arch)
	if err != nil {
		return fmt.Errorf("acquire binary: %w", err)
	}
	defer func() { _ = binary.Close() }()

	tmpFile, err := os.CreateTemp("", "devsy-agent-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := io.Copy(tmpFile, binary); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("write temp binary: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}

	dest := fmt.Sprintf("%s:%s", d.ContainerID, destPath)
	out, err := d.cmd(ctx, "cp", tmpPath, dest).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}
	return nil
}

func (d *RemoteDockerDelivery) chmodBinary(ctx context.Context, destPath string) error {
	out, err := d.cmd(ctx, "exec", d.ContainerID, "chmod", "755", destPath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}
	return nil
}

func (d *RemoteDockerDelivery) ensureDir(ctx context.Context, filePath string) error {
	dir := path.Dir(filePath)
	out, err := d.cmd(ctx, "exec", d.ContainerID, "mkdir", "-p", dir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}
	return nil
}

func (d *RemoteDockerDelivery) cmd(ctx context.Context, args ...string) *exec.Cmd {
	// #nosec G204 -- args are constructed internally, not from user input
	cmd := exec.CommandContext(ctx, d.dockerCommand(), args...)
	if d.Environment != nil {
		cmd.Env = append(os.Environ(), d.Environment...)
	}
	return cmd
}

func (d *RemoteDockerDelivery) dockerCommand() string {
	if d.DockerCommand != "" {
		return d.DockerCommand
	}
	return defaultDockerCmd
}
