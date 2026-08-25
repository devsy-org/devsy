package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	containerRestartAttempts = 3

	// snapshotImageLabel marks a committed image as a devsy workspace snapshot,
	// so it's identifiable via `docker inspect`/`docker images --filter`.
	snapshotImageLabel = "sh.devsy.snapshot=true"
)

func (d *dockerDriver) CommandDevContainer(
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

	args := []string{dockerExec}
	if params.Stdin != nil {
		args = append(args, "-i")
	}
	args = append(args, "-u", params.User, container.ID, "sh", "-c", params.Command)
	err = d.Docker.Run(ctx, args, docker.Streams{
		Stdin:  params.Stdin,
		Stdout: params.Stdout,
		Stderr: params.Stderr,
	})
	if err != nil {
		return fmt.Errorf("run command in container: %w", err)
	}
	return nil
}

// ensureContainerRunning checks the container's state and starts it if necessary.
func (d *dockerDriver) ensureContainerRunning(
	ctx context.Context,
	container *config.ContainerDetails,
) error {
	status := container.State.Status
	switch status {
	case config.ContainerStatusRunning:
		return nil
	case config.ContainerStatusDead, config.ContainerStatusRemoving:
		return fmt.Errorf(
			"%w: container %s is %q",
			docker.ErrContainerTerminal,
			container.ID,
			status,
		)
	case config.ContainerStatusPaused:
		return d.unpauseAndWait(ctx, container)
	case config.ContainerStatusRestarting:
		return d.waitForRestart(ctx, container)
	case config.ContainerStatusExited, config.ContainerStatusCreated:
		return d.restartAndWait(ctx, container, status)
	default:
		return fmt.Errorf(
			"%w: container %s is in unknown state %q",
			docker.ErrContainerTerminal,
			container.ID,
			status,
		)
	}
}

// restartAndWait starts the container and waits for it to be running,
// retrying up to containerRestartAttempts times. It aborts immediately when
// the container enters a terminal state.
func (d *dockerDriver) restartAndWait(
	ctx context.Context,
	container *config.ContainerDetails,
	status config.ContainerStatus,
) error {
	var lastErr error
	for attempt := 1; attempt <= containerRestartAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		log.Infof(
			"restarting container %s (status=%s, attempt=%d/%d)",
			container.ID, status, attempt, containerRestartAttempts,
		)
		if err := d.Docker.StartContainer(ctx, container.ID); err != nil {
			lastErr = fmt.Errorf("start container: %w", err)
		} else if err := d.Docker.WaitContainerRunning(ctx, container.ID); err != nil {
			lastErr = fmt.Errorf("wait for container to be running: %w", err)
		} else {
			log.Infof("container %s is running", container.ID)
			return nil
		}
		if errors.Is(lastErr, docker.ErrContainerTerminal) ||
			errors.Is(lastErr, context.Canceled) ||
			errors.Is(lastErr, context.DeadlineExceeded) {
			return lastErr
		}
		log.Debugf("container %s restart attempt %d failed: %v", container.ID, attempt, lastErr)
	}

	return fmt.Errorf(
		"%w: container %s did not stay running after %d attempts: %w",
		docker.ErrContainerTerminal, container.ID, containerRestartAttempts, lastErr,
	)
}

// unpauseAndWait unpauses a paused container and waits for it to be running.
func (d *dockerDriver) unpauseAndWait(
	ctx context.Context,
	container *config.ContainerDetails,
) error {
	log.Infof("unpausing container %s", container.ID)
	if err := d.Docker.UnpauseContainer(ctx, container.ID); err != nil {
		return fmt.Errorf("unpause container: %w", err)
	}
	if err := d.Docker.WaitContainerRunning(ctx, container.ID); err != nil {
		return fmt.Errorf("wait for container to be running: %w", err)
	}
	log.Infof("container %s is running", container.ID)
	return nil
}

// waitForRestart lets the daemon finish an in-flight restart before acting:
// DockerHelper.StartContainer rejects containers still in the "restarting"
// state. Once the container settles, either it is already running or it has
// stopped again (ErrContainerExited) and needs an explicit start.
func (d *dockerDriver) waitForRestart(
	ctx context.Context,
	container *config.ContainerDetails,
) error {
	log.Infof("container %s is restarting, waiting for a stable state", container.ID)
	err := d.Docker.WaitContainerRunning(ctx, container.ID)
	switch {
	case err == nil:
		log.Infof("container %s is running", container.ID)
		return nil
	case errors.Is(err, docker.ErrContainerExited):
		return d.restartAndWait(ctx, container, config.ContainerStatusExited)
	default:
		return err
	}
}

func (d *dockerDriver) PushDevContainer(ctx context.Context, image string) error {
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	args := []string{
		"push",
		image,
	}

	log.Debugf(
		"running docker push command: command=%s, args=%s",
		d.Docker.DockerCommand,
		strings.Join(args, " "),
	)
	err := d.Docker.Run(ctx, args, docker.Streams{Stdout: writer, Stderr: writer})
	if err != nil {
		return fmt.Errorf("push image: %w", err)
	}

	return nil
}

func (d *dockerDriver) TagDevContainer(ctx context.Context, image, tag string) error {
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	args := []string{
		"tag",
		image,
		tag,
	}

	log.Debugf(
		"running docker tag command: command=%s, args=%s",
		d.Docker.DockerCommand,
		strings.Join(args, " "),
	)
	err := d.Docker.Run(ctx, args, docker.Streams{Stdout: writer, Stderr: writer})
	if err != nil {
		return fmt.Errorf("tag image: %w", err)
	}

	return nil
}

func (d *dockerDriver) CommitContainer(ctx context.Context, workspaceID, tag string) error {
	container, err := d.FindDevContainer(ctx, workspaceID)
	if err != nil {
		return err
	} else if container == nil {
		return fmt.Errorf("container not found: workspaceID=%s", workspaceID)
	}

	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	args := []string{"commit", "--change", "LABEL " + snapshotImageLabel, container.ID, tag}

	log.Debugf(
		"running docker commit command: command=%s, args=%s",
		d.Docker.DockerCommand,
		strings.Join(args, " "),
	)
	if err := d.Docker.Run(ctx, args, docker.Streams{Stdout: writer, Stderr: writer}); err != nil {
		return fmt.Errorf("commit container %s: %w", container.ID, err)
	}

	return nil
}

// DeleteDevContainer stops a still-running container before removing it,
// since podman/docker refuse to `rm` a running container.
func (d *dockerDriver) DeleteDevContainer(ctx context.Context, workspaceId string) error {
	container, err := d.FindDevContainer(ctx, workspaceId)
	if err != nil {
		return err
	} else if container == nil {
		return nil
	}

	if container.State.Status == config.ContainerStatusRunning {
		if err := d.Docker.Stop(ctx, container.ID); err != nil {
			log.Warnf("stop before delete failed for %s: %v", container.ID, err)
		}
	}

	return d.Docker.Remove(ctx, container.ID)
}

func (d *dockerDriver) StartDevContainer(ctx context.Context, workspaceId string) error {
	container, err := d.FindDevContainer(ctx, workspaceId)
	if err != nil {
		return err
	} else if container == nil {
		return fmt.Errorf("container not found")
	}

	return d.Docker.StartContainer(ctx, container.ID)
}

func (d *dockerDriver) StopDevContainer(ctx context.Context, workspaceId string) error {
	container, err := d.FindDevContainer(ctx, workspaceId)
	if err != nil {
		return err
	} else if container == nil {
		return fmt.Errorf("container not found")
	}

	return d.Docker.Stop(ctx, container.ID)
}

func (d *dockerDriver) InspectImage(
	ctx context.Context,
	imageName string,
) (*config.ImageDetails, error) {
	return d.Docker.InspectImage(ctx, imageName, true)
}

func (d *dockerDriver) GetImageTag(ctx context.Context, imageID string) (string, error) {
	return d.Docker.GetImageTag(ctx, imageID)
}

func (d *dockerDriver) GetDevContainerLogs(
	ctx context.Context,
	workspaceId string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	container, err := d.FindDevContainer(ctx, workspaceId)
	if err != nil {
		return err
	} else if container == nil {
		return fmt.Errorf("container not found")
	}

	return d.Docker.GetContainerLogs(ctx, container.ID, stdout, stderr)
}

func (d *dockerDriver) RunImageDevContainer(
	ctx context.Context,
	params *driver.RunImageDevContainerParams,
) error {
	if err := d.EnsureImage(ctx, params.Options); err != nil {
		return err
	}

	helper, err := d.DockerHelper()
	if err != nil {
		return err
	}

	args, err := d.buildRunArgs(params, helper)
	if err != nil {
		return err
	}

	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	if err := d.startContainer(ctx, params.LocalWorkspaceFolder, args, writer); err != nil {
		return err
	}

	return d.UpdateContainerUserUID(ctx, params.WorkspaceID, params.ParsedConfig, writer)
}

var (
	imageInspectPollInterval = 500 * time.Millisecond
	imageInspectPollTimeout  = 10 * time.Second
)

func (d *dockerDriver) EnsureImage(
	ctx context.Context,
	options *driver.RunOptions,
) error {
	log.Infof("inspecting image: image=%s", options.Image)
	err := d.inspectImage(ctx, options)
	if err == nil {
		return nil
	}
	if !errors.Is(err, docker.ErrImageNotFound) {
		return fmt.Errorf("inspect image %s: %w", options.Image, err)
	}
	if options.ImageBuilt {
		// A devsy-built, locally-tagged image can never be resolved by a
		// registry pull: no registry has it, and a same-named external tag
		// would run an entirely different image than the one that was built.
		return fmt.Errorf("inspect image %s: %w", options.Image, err)
	}

	log.Infof("image not found, pulling image: image=%s", options.Image)
	writer := log.Writer(log.LevelDebug)
	defer func() { _ = writer.Close() }()

	return d.Docker.Pull(ctx, docker.PullOptions{
		Image:    options.Image,
		Platform: options.Platform,
		Stdout:   writer,
		Stderr:   writer,
	})
}

// inspectImage inspects the given image; if options.ImageBuilt is true, it retries while the only failure is
// ErrImageNotFound, otherwise it returns immediately on any error.
func (d *dockerDriver) inspectImage(ctx context.Context, options *driver.RunOptions) error {
	if !options.ImageBuilt {
		_, err := d.Docker.InspectImage(ctx, options.Image, false)
		return err
	}
	return d.waitForLocalImage(ctx, options.Image)
}

// waitForLocalImage inspects image, retrying while the only failure is
// ErrImageNotFound. Any other inspect error (e.g. daemon unreachable) stops
// the retry immediately. Returns nil once found, or the last ErrImageNotFound
// once imageInspectPollTimeout elapses without the image appearing.
func (d *dockerDriver) waitForLocalImage(ctx context.Context, image string) error {
	var lastErr error
	pollErr := wait.PollUntilContextTimeout(
		ctx, imageInspectPollInterval, imageInspectPollTimeout, true,
		func(ctx context.Context) (bool, error) {
			_, err := d.Docker.InspectImage(ctx, image, false)
			if err == nil {
				return true, nil
			}
			if errors.Is(err, docker.ErrImageNotFound) {
				lastErr = err
				return false, nil
			}
			return false, err
		},
	)
	if pollErr != nil && lastErr != nil {
		return lastErr
	}
	return pollErr
}

func (d *dockerDriver) startContainer(
	ctx context.Context,
	dir string,
	args []string,
	writer io.Writer,
) error {
	log.Infof(
		"running docker command: command=%s, args=%s, cwd=%s",
		d.Docker.DockerCommand,
		strings.Join(args, " "),
		dir,
	)

	logHostEnvOnce(ctx, d.Docker)
	logBindSources(args)

	err := d.Docker.RunWithDir(ctx, dir, args, docker.Streams{Stdout: writer, Stderr: writer})
	if err != nil {
		log.Errorf(
			"docker container failed to start: error=%v, command=%s, args=%s, cwd=%s",
			err,
			d.Docker.DockerCommand,
			strings.Join(args, " "),
			dir,
		)
		return fmt.Errorf("failed to start dev container: %w", err)
	}
	return nil
}
