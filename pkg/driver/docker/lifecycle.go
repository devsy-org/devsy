package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
)

const containerRestartAttempts = 3

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
	return d.Docker.Run(ctx, args, docker.Streams{
		Stdin:  params.Stdin,
		Stdout: params.Stdout,
		Stderr: params.Stderr,
	})
}

// ensureContainerRunning checks that the given container is running, and if
// not, attempts to start it and wait for it to be running. If the container is
// in a terminal state (dead or removing), it returns an error.
func (d *dockerDriver) ensureContainerRunning(
	ctx context.Context,
	container *config.ContainerDetails,
) error {
	status := strings.ToLower(container.State.Status)
	if status == "dead" || status == "removing" {
		return fmt.Errorf(
			"%w: container %s is %q",
			docker.ErrContainerTerminal,
			container.ID,
			status,
		)
	}
	if status == "running" {
		return nil
	}

	var lastErr error
	for attempt := 1; attempt <= containerRestartAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		log.Infof(
			"container %s is not running (status=%s), restarting (attempt %d/%d)",
			container.ID, status, attempt, containerRestartAttempts,
		)
		err := d.restartAndWait(ctx, container.ID)
		if err == nil {
			log.Infof("container %s is now running", container.ID)
			return nil
		}
		if errors.Is(err, docker.ErrContainerTerminal) {
			return err
		}
		lastErr = err
		log.Debugf("container %s restart attempt %d failed: %v", container.ID, attempt, err)
	}

	return fmt.Errorf(
		"%w: container %s did not stay running after %d restart attempts: %v",
		docker.ErrContainerTerminal, container.ID, containerRestartAttempts, lastErr,
	)
}

func (d *dockerDriver) restartAndWait(ctx context.Context, containerID string) error {
	if err := d.Docker.StartContainer(ctx, containerID); err != nil {
		return fmt.Errorf("restart container: %w", err)
	}
	if err := d.Docker.WaitContainerRunning(ctx, containerID); err != nil {
		return fmt.Errorf("wait for container to be running: %w", err)
	}
	return nil
}

func (d *dockerDriver) PushDevContainer(ctx context.Context, image string) error {
	// push image
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	// build args
	args := []string{
		"push",
		image,
	}

	// run command
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
	// Tag image
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	// build args
	args := []string{
		"tag",
		image,
		tag,
	}

	// run command
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

func (d *dockerDriver) DeleteDevContainer(ctx context.Context, workspaceId string) error {
	container, err := d.FindDevContainer(ctx, workspaceId)
	if err != nil {
		return err
	} else if container == nil {
		return nil
	}

	err = d.Docker.Remove(ctx, container.ID)
	if err != nil {
		return err
	}

	return nil
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

func (d *dockerDriver) RunDevContainer(
	ctx context.Context,
	workspaceId string,
	options *driver.RunOptions,
) error {
	return fmt.Errorf("unsupported")
}

func (d *dockerDriver) RunDockerDevContainer(
	ctx context.Context,
	params *driver.RunDockerDevContainerParams,
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

func (d *dockerDriver) EnsureImage(
	ctx context.Context,
	options *driver.RunOptions,
) error {
	log.Infof("inspecting image: image=%s", options.Image)
	_, err := d.Docker.InspectImage(ctx, options.Image, false)
	if err == nil {
		return nil
	}
	if !errors.Is(err, docker.ErrImageNotFound) {
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
