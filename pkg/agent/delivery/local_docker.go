package delivery

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/version"
)

var _ AgentDelivery = (*LocalDockerDelivery)(nil)

const (
	defaultDockerCmd = "docker"
	podmanCmd        = "podman"
	volumePrefix     = "devsy-agent-"
	volumeMountPath  = "/opt/devsy"

	// cmdRun / flagRM build throwaway helper-container invocations.
	cmdRun = "run"
	flagRM = "--rm"
)

type LocalDockerDelivery struct {
	DockerCommand   string
	Environment     []string
	HelperImage     string
	ExpectedVersion string
}

func (d *LocalDockerDelivery) Phase() DeliveryPhase {
	return PhasePreStart
}

func (d *LocalDockerDelivery) DeliverPreStart(ctx context.Context, opts PreStartOptions) error {
	if opts.BinarySource == nil {
		return fmt.Errorf("binary source is required for local docker delivery")
	}

	volumeName := volumePrefix + opts.WorkspaceID

	labels := pkgconfig.DockerVolumeLabels(opts.WorkspaceID, pkgconfig.VolumeRoleAgent)
	if err := d.createVolume(ctx, volumeName, labels); err != nil {
		return fmt.Errorf("create agent volume: %w", err)
	}

	if err := d.ensureCurrentBinary(ctx, volumeName, opts.BinarySource, opts.Arch); err != nil {
		return err
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

// Cleanup removes every devsy-managed volume owned by the workspace (the agent
// volume and any seeded workspace volume), identified by labels. Only labeled
// volumes are removed, so foreign/external volumes are left untouched.
func (d *LocalDockerDelivery) Cleanup(ctx context.Context, workspaceID string) error {
	volumes, err := d.listManagedVolumes(ctx, workspaceID)
	if err != nil {
		return err
	}
	// Attempt every volume so one transient failure does not orphan the rest.
	var errs []error
	for _, name := range volumes {
		if err := d.removeVolume(ctx, name); err != nil {
			errs = append(errs, err)
			continue
		}
		log.Infof("removed devsy-managed volume: %s", name)
	}
	return errors.Join(errs...)
}

func (d *LocalDockerDelivery) ensureCurrentBinary(
	ctx context.Context,
	volumeName string,
	binarySource BinarySourceFunc,
	arch string,
) error {
	expected := d.expectedVersion()
	actual := d.detectVolumeVersion(ctx, volumeName)

	if actual != "" && actual == expected {
		log.Debugf(
			"remote agent version matches expected version %s, skipping delivery",
			expected,
		)
		return nil
	}

	if actual != "" {
		log.Infof("upgraded remote agent from %s to %s", actual, expected)
	}

	if err := d.populateVolume(ctx, volumeName, binarySource, arch); err != nil {
		if removeErr := d.removeVolume(ctx, volumeName); removeErr != nil {
			log.Debugf("failed to clean up volume after populate failure: %v", removeErr)
		}
		return fmt.Errorf("populate agent volume: %w", err)
	}
	return nil
}

func (d *LocalDockerDelivery) createVolume(
	ctx context.Context,
	name string,
	labels map[string]string,
) error {
	args := append([]string{"volume", "create"}, pkgconfig.LabelArgs(labels)...)
	args = append(args, name)
	out, err := d.cmd(ctx, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}
	return nil
}

func (d *LocalDockerDelivery) helperImageName() string {
	return pkgconfig.HelperImage(d.HelperImage)
}

func (d *LocalDockerDelivery) expectedVersion() string {
	if d.ExpectedVersion != "" {
		return d.ExpectedVersion
	}
	return version.GetVersion()
}

func (d *LocalDockerDelivery) detectVolumeVersion(ctx context.Context, volumeName string) string {
	binaryPath := volumeMountPath + "/" + binaryName()
	script := fmt.Sprintf(
		`[ -x "%s" ] && "%s" --version 2>/dev/null || true`,
		binaryPath, binaryPath,
	)
	args := []string{
		cmdRun, flagRM,
		"-v", volumeName + ":" + volumeMountPath,
		d.helperImageName(),
		"sh", "-c", script,
	}

	out, err := d.cmd(ctx, args...).Output()
	if err != nil {
		log.Debugf("failed to detect agent version in volume: %v", err)
		return ""
	}
	return strings.TrimSpace(string(out))
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
		cmdRun, flagRM,
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

// populateVolumeDirectCopy writes the agent binary to a volume mountpoint the
// host can write directly (docker, or rootful podman).
//
// NOTE: Podman rootless volumes are owned by a mapped UID, so a direct host write
// fails with EACCES. `podman unshare` re-enters the container's user
// namespace to write the mountpoint. Rootful podman and docker write directly.
func (d *LocalDockerDelivery) populateVolumeDirectCopy(
	ctx context.Context,
	volumeName string,
	data []byte,
) error {
	mountpoint, err := d.volumeMountpoint(ctx, volumeName)
	if err != nil {
		return fmt.Errorf("inspect volume mountpoint: %w", err)
	}

	destPath := filepath.Join(mountpoint, binaryName())

	if err := writeBinaryDirect(destPath, data); err == nil {
		return nil
	} else if !d.isPodman() || !errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("write binary to volume: %w", err)
	}

	return d.populateVolumeViaUnshare(ctx, destPath, data)
}

// writeBinaryDirect writes the agent binary to a volume mountpoint the host
// can write directly (docker, or rootful podman).
func writeBinaryDirect(destPath string, data []byte) error {
	if err := os.WriteFile(destPath, data, 0o600); err != nil {
		return err
	}
	// #nosec G302 -- agent binary must be executable
	return os.Chmod(destPath, 0o755)
}

func (d *LocalDockerDelivery) populateVolumeViaUnshare(
	ctx context.Context,
	destPath string,
	data []byte,
) error {
	cmd := d.cmd(ctx, "unshare", "sh", "-c", `cat > "$1" && chmod 755 "$1"`, "--", destPath)
	cmd.Stdin = bytes.NewReader(data)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("podman unshare write failed: %s: %w", string(out), err)
	}
	return nil
}

func (d *LocalDockerDelivery) isPodman() bool {
	return filepath.Base(d.dockerCommand()) == podmanCmd
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

// removeVolume force-removes a single named volume. It is safe to call for a
// non-existent volume since removal is forced.
func (d *LocalDockerDelivery) removeVolume(ctx context.Context, name string) error {
	out, err := d.cmd(ctx, "volume", "rm", "-f", name).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}
	return nil
}

// listManagedVolumes returns the names of devsy-managed volumes owned by the
// given workspace, identified by labels. Only labeled volumes are returned, so
// foreign (user-created) volumes are never included.
func (d *LocalDockerDelivery) listManagedVolumes(
	ctx context.Context,
	workspaceID string,
) ([]string, error) {
	out, err := d.cmd(ctx,
		"volume", "ls", "--quiet",
		"--filter", "label="+pkgconfig.DockerManagedLabel+"="+pkgconfig.LabelValueTrue,
		"--filter", "label="+pkgconfig.DockerWorkspaceIDLabel+"="+workspaceID,
	).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", string(out), err)
	}
	var names []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
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
	return pkgconfig.ContainerDevsyHelperLocation[len("/usr/local/bin/"):]
}
