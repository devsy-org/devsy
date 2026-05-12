package delivery

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

var _ AgentDelivery = (*LocalDockerDelivery)(nil)

const (
	defaultDockerCmd = "docker"
	volumePrefix     = "devsy-agent-"
	volumeMountPath  = "/opt/devsy"
	helperImage      = "busybox:latest"
)

type LocalDockerDelivery struct {
	DockerCommand string
	Environment   []string
}

func (d *LocalDockerDelivery) Phase() DeliveryPhase {
	return PhasePreStart
}

func (d *LocalDockerDelivery) DeliverPreStart(ctx context.Context, opts PreStartOptions) error {
	if opts.BinarySource == nil {
		return fmt.Errorf("binary source is required for local docker delivery")
	}

	volumeName := volumePrefix + opts.WorkspaceID

	if err := d.createVolume(ctx, volumeName); err != nil {
		return fmt.Errorf("create agent volume: %w", err)
	}

	if err := d.populateVolume(ctx, volumeName, opts.BinarySource, opts.Arch); err != nil {
		if removeErr := d.removeVolume(ctx, volumeName); removeErr != nil {
			log.Debugf("failed to clean up volume after populate failure: %v", removeErr)
		}
		return fmt.Errorf("populate agent volume: %w", err)
	}

	opts.RunOptions.Mounts = append(opts.RunOptions.Mounts, &config.Mount{
		Type:   "volume",
		Source: volumeName,
		Target: volumeMountPath,
	})

	if opts.RunOptions.Env == nil {
		opts.RunOptions.Env = make(map[string]string)
	}
	opts.RunOptions.Env["DEVSY_AGENT_PATH"] = volumeMountPath + "/" + binaryName()

	return nil
}

func (d *LocalDockerDelivery) DeliverPostStart(_ context.Context, _ PostStartOptions) error {
	return fmt.Errorf("LocalDockerDelivery does not support post-start delivery")
}

func (d *LocalDockerDelivery) Cleanup(ctx context.Context, workspaceID string) error {
	return d.removeVolume(ctx, workspaceID)
}

func (d *LocalDockerDelivery) createVolume(ctx context.Context, name string) error {
	out, err := d.cmd(ctx, "volume", "create", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}
	return nil
}

func (d *LocalDockerDelivery) populateVolume(
	ctx context.Context,
	volumeName string,
	binarySource BinarySourceFunc,
	arch string,
) error {
	binary, err := binarySource(ctx, arch)
	if err != nil {
		return fmt.Errorf("acquire binary: %w", err)
	}
	defer func() { _ = binary.Close() }()

	containerName := "devsy-agent-init-" + volumeName
	script := fmt.Sprintf(
		"cat > %s/%s && chmod 755 %s/%s",
		volumeMountPath, binaryName(), volumeMountPath, binaryName(),
	)
	args := []string{
		"run", "--rm",
		"--name", containerName,
		"-v", volumeName + ":" + volumeMountPath,
		"-i",
		helperImage,
		"sh", "-c", script,
	}

	cmd := d.cmd(ctx, args...)
	cmd.Stdin = binary

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}
	return nil
}

func (d *LocalDockerDelivery) removeVolume(ctx context.Context, workspaceID string) error {
	volumeName := volumePrefix + workspaceID
	out, err := d.cmd(ctx, "volume", "rm", "-f", volumeName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}
	return nil
}

func (d *LocalDockerDelivery) cmd(ctx context.Context, args ...string) *exec.Cmd {
	// #nosec G204 -- args are constructed internally, not from user input
	cmd := exec.CommandContext(ctx, d.dockerCommand(), args...)
	if d.Environment != nil {
		cmd.Env = append(os.Environ(), d.Environment...)
	}
	return cmd
}

func (d *LocalDockerDelivery) dockerCommand() string {
	if d.DockerCommand != "" {
		return d.DockerCommand
	}
	return defaultDockerCmd
}

func binaryName() string {
	return agent.ContainerDevsyHelperLocation[len("/usr/local/bin/"):]
}
