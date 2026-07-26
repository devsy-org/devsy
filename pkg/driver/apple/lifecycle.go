package apple

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/devsy-org/devsy/pkg/apple"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
)

func (d *appleDriver) CommandDevContainer(
	ctx context.Context,
	params *driver.CommandParams,
) error {
	container, err := d.FindDevContainer(ctx, params.WorkspaceID)
	if err != nil {
		return err
	} else if container == nil {
		return fmt.Errorf("container not found")
	}

	if err := d.ensureContainerRunning(ctx, container); err != nil {
		return err
	}

	args := []string{appleExec}
	if params.Stdin != nil {
		args = append(args, "-i")
	}
	if params.User != "" {
		args = append(args, "-u", params.User)
	}
	args = append(args, container.ID, "sh", "-c", params.Command)
	return d.Apple.Run(ctx, args, apple.Streams{
		Stdin: params.Stdin, Stdout: params.Stdout, Stderr: params.Stderr,
	})
}

func (d *appleDriver) ensureContainerRunning(
	ctx context.Context,
	container *config.ContainerDetails,
) error {
	if strings.ToLower(container.State.Status) == statusRunning {
		return nil
	}
	if err := d.Apple.StartContainer(ctx, container.ID); err != nil {
		return err
	}
	return d.Apple.WaitContainerRunning(ctx, container.ID)
}

func (d *appleDriver) StartDevContainer(ctx context.Context, workspaceID string) error {
	container, err := d.FindDevContainer(ctx, workspaceID)
	if err != nil {
		return err
	} else if container == nil {
		return fmt.Errorf("container not found")
	}
	return d.Apple.StartContainer(ctx, container.ID)
}

func (d *appleDriver) StopDevContainer(ctx context.Context, workspaceID string) error {
	container, err := d.FindDevContainer(ctx, workspaceID)
	if err != nil {
		return err
	} else if container == nil {
		return fmt.Errorf("container not found")
	}
	return d.Apple.Stop(ctx, container.ID)
}

func (d *appleDriver) DeleteDevContainer(ctx context.Context, workspaceID string) error {
	container, err := d.FindDevContainer(ctx, workspaceID)
	if err != nil {
		return err
	} else if container == nil {
		return nil
	}
	// Apple's `delete` requires a stopped container. `container stop` is synchronous;
	// a stop failure is logged but delete is still attempted (it fails loudly if the
	// container is genuinely still running).
	if strings.ToLower(container.State.Status) == statusRunning {
		if err := d.Apple.Stop(ctx, container.ID); err != nil {
			log.Warnf("stop before delete failed for %s: %v", container.ID, err)
		}
	}
	return d.Apple.Remove(ctx, container.ID)
}

func (d *appleDriver) GetDevContainerLogs(
	ctx context.Context,
	workspaceID string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	container, err := d.FindDevContainer(ctx, workspaceID)
	if err != nil {
		return err
	} else if container == nil {
		return fmt.Errorf("container not found")
	}
	return d.Apple.GetContainerLogs(ctx, container.ID, stdout, stderr)
}

func (d *appleDriver) InspectImage(
	ctx context.Context,
	imageName string,
) (*config.ImageDetails, error) {
	return d.Apple.InspectImage(ctx, imageName, true)
}

func (d *appleDriver) GetImageTag(ctx context.Context, imageID string) (string, error) {
	return d.Apple.GetImageTag(ctx, imageID)
}

func (d *appleDriver) PushDevContainer(ctx context.Context, image string) error {
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()
	if err := d.Apple.Push(ctx, image, writer, writer); err != nil {
		return fmt.Errorf("push image: %w", err)
	}
	return nil
}

func (d *appleDriver) TagDevContainer(ctx context.Context, image, tag string) error {
	if err := d.Apple.Tag(ctx, image, tag); err != nil {
		return fmt.Errorf("tag image: %w", err)
	}
	return nil
}

func (d *appleDriver) RunImageDevContainer(
	ctx context.Context,
	params *driver.RunImageDevContainerParams,
) error {
	if err := d.EnsureImage(ctx, params.Options); err != nil {
		return err
	}

	args := d.buildRunArgs(params)

	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	log.Infof(
		"running apple container command: command=%s, args=%s",
		d.command, redactArgs(args),
	)
	err := d.Apple.RunWithDir(ctx, params.LocalWorkspaceFolder, args,
		apple.Streams{Stdout: writer, Stderr: writer})
	if err != nil {
		return fmt.Errorf("failed to start dev container: %w", err)
	}

	return d.UpdateContainerUserUID(ctx, params.WorkspaceID, params.ParsedConfig, writer)
}

func (d *appleDriver) EnsureImage(ctx context.Context, options *driver.RunOptions) error {
	log.Infof("inspecting image: image=%s", options.Image)
	if details, err := d.Apple.InspectImage(
		ctx,
		options.Image,
		false,
	); err == nil &&
		details != nil {
		return nil
	}

	log.Infof("image not found, pulling image: image=%s", options.Image)
	writer := log.Writer(log.LevelDebug)
	defer func() { _ = writer.Close() }()
	return d.Apple.Pull(ctx, apple.PullOptions{
		Image:    options.Image,
		Platform: options.Platform,
		Stdout:   writer,
		Stderr:   writer,
	})
}

// UpdateContainerUserUID is a no-op: each Apple container is its own VM and
// virtio-fs handles host-mount ownership, so UID/GID remapping does not apply.
func (d *appleDriver) UpdateContainerUserUID(
	ctx context.Context,
	workspaceID string,
	parsedConfig *config.DevContainerConfig,
	writer io.Writer,
) error {
	return nil
}
