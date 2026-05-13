package delivery

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
)

var _ AgentDelivery = (*LocalDockerDelivery)(nil)

const (
	defaultDockerCmd   = "docker"
	volumePrefix       = "devsy-agent-"
	volumeMountPath    = "/opt/devsy"
	defaultHelperImage = "busybox:latest"
)

type LocalDockerDelivery struct {
	DockerCommand string
	Environment   []string
	HelperImage   string
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

func (d *LocalDockerDelivery) helperImageName() string {
	if d.HelperImage != "" {
		return d.HelperImage
	}
	return defaultHelperImage
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

	data, err := io.ReadAll(binary)
	if err != nil {
		return fmt.Errorf("read binary: %w", err)
	}

	err = d.populateVolumeWithHelper(ctx, volumeName, bytes.NewReader(data))
	if err == nil {
		return nil
	}
	log.Debugf("helper container populate failed, trying direct copy: %v", err)

	return d.populateVolumeDirectCopy(ctx, volumeName, data)
}

func (d *LocalDockerDelivery) populateVolumeWithHelper(
	ctx context.Context,
	volumeName string,
	binary io.Reader,
) error {
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
		d.helperImageName(),
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

func (d *LocalDockerDelivery) populateVolumeDirectCopy(
	ctx context.Context,
	volumeName string,
	data []byte,
) error {
	if d.isRootlessPodman(ctx) {
		return d.populateVolumeUnshare(ctx, volumeName, data)
	}

	mountpoint, err := d.volumeMountpoint(ctx, volumeName)
	if err != nil {
		return fmt.Errorf("inspect volume mountpoint: %w", err)
	}

	destPath := filepath.Join(mountpoint, binaryName())

	if err := os.WriteFile(destPath, data, 0o600); err != nil {
		return fmt.Errorf("write binary to volume: %w", err)
	}
	// #nosec G302 -- agent binary must be executable
	if err := os.Chmod(destPath, 0o755); err != nil {
		return fmt.Errorf("chmod binary: %w", err)
	}

	return nil
}

func (d *LocalDockerDelivery) populateVolumeUnshare(
	ctx context.Context,
	volumeName string,
	data []byte,
) error {
	mountpoint, err := d.volumeMountpoint(ctx, volumeName)
	if err != nil {
		return fmt.Errorf("inspect volume mountpoint: %w", err)
	}

	destPath := filepath.Join(mountpoint, binaryName())

	script := fmt.Sprintf(
		"cat > %s && chmod 755 %s",
		destPath, destPath,
	)
	// #nosec G204 -- args are constructed internally, not from user input
	cmd := exec.CommandContext(ctx, d.dockerCommand(), "unshare", "sh", "-c", script)
	cmd.Stdin = bytes.NewReader(data)
	if d.Environment != nil {
		cmd.Env = append(os.Environ(), d.Environment...)
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("podman unshare write: %s: %w", string(out), err)
	}
	return nil
}

func (d *LocalDockerDelivery) volumeMountpoint(
	ctx context.Context,
	volumeName string,
) (string, error) {
	out, err := d.cmd(
		ctx, "volume", "inspect",
		"--format", "{{.Mountpoint}}",
		volumeName,
	).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w", string(out), err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (d *LocalDockerDelivery) removeVolume(ctx context.Context, workspaceID string) error {
	volumeName := volumePrefix + workspaceID
	out, err := d.cmd(ctx, "volume", "rm", "-f", volumeName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}
	return nil
}

func (d *LocalDockerDelivery) isPodman(ctx context.Context) bool {
	out, err := d.cmd(ctx, "--version").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "podman")
}

func (d *LocalDockerDelivery) isRootlessPodman(ctx context.Context) bool {
	if !d.isPodman(ctx) {
		return false
	}
	out, err := d.cmd(ctx, "info", "--format", "{{.Host.Security.Rootless}}").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) == "true"
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
