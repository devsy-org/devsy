package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/scanner"
	"k8s.io/apimachinery/pkg/util/wait"
)

// DockerBuilder represents the Docker builder types.
type DockerBuilder int

// Enum values for DockerBuilder.
const (
	DockerBuilderDefault DockerBuilder = iota
	DockerBuilderBuildX
	DockerBuilderBuildKit
)

const (
	containerRunningPollInterval = 500 * time.Millisecond
	containerRunningTimeout      = 30 * time.Second
	containerExitGrace           = 2 * time.Second
)

var (
	ErrContainerTerminal = errors.New("container in terminal state")
	ErrContainerExited   = errors.New("container exited after start")
)

func (db DockerBuilder) String() string {
	return [...]string{"", "buildx", "buildkit"}[db]
}

func DockerBuilderFromString(s string) (DockerBuilder, error) {
	switch s {
	case "":
		return DockerBuilderDefault, nil
	case "buildkit":
		return DockerBuilderBuildKit, nil
	case "buildx":
		return DockerBuilderBuildX, nil
	default:
		return DockerBuilderDefault, errors.New("invalid docker builder")
	}
}

type DockerHelper struct {
	DockerCommand string
	// for a running container, we cannot pass down the container ID to the driver without introducing
	// changes in the driver interface (which we do not want to do). So, to get around this, we pass
	// it down to the driver during docker helper initialization.
	ContainerID string
	// allow command to have a custom environment
	Environment []string
	Builder     DockerBuilder
	Runtime     ContainerRuntime
	// Elevator, when set, runs docker commands through a privilege-elevation
	// helper (pkexec/sudo/doas). Nil disables elevation.
	Elevator *Elevator
}

func (r *DockerHelper) GPUSupportEnabled() (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return r.GetRuntime().GPUAvailable(ctx, r)
}

// GetRuntime returns the container runtime for this helper.
// If no runtime was explicitly set, it auto-detects from the docker command.
func (r *DockerHelper) GetRuntime() ContainerRuntime {
	if r.Runtime != nil {
		return r.Runtime
	}
	return DetectRuntime(r.DockerCommand)
}

// ClientVersion returns the docker CLI version (e.g. "29.5.3"), or "".
func (r *DockerHelper) ClientVersion(ctx context.Context) string {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := r.buildCmd(cctx, "version", "--format", "{{.Client.Version}}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (r *DockerHelper) FindDevContainer(
	ctx context.Context,
	labels []string,
) (*config.ContainerDetails, error) {
	containers, err := r.FindContainer(ctx, labels)
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	} else if len(containers) == 0 {
		return nil, nil
	}

	return r.FindContainerByID(ctx, containers)
}

func (r *DockerHelper) FindContainerByID(
	ctx context.Context,
	containerIds []string,
) (*config.ContainerDetails, error) {
	containerDetails, err := r.InspectContainers(ctx, containerIds)
	if err != nil {
		return nil, err
	}

	// find matching container
	for _, details := range containerDetails {
		if strings.ToLower(details.State.Status) != "removing" {
			details.State.Status = strings.ToLower(details.State.Status)
			return &details, nil
		}
	}

	return nil, nil
}

func (r *DockerHelper) DeleteVolume(ctx context.Context, volume string) error {
	if volume == "" {
		return nil
	}

	// If volume does not exist, just exit
	out, err := r.buildCmd(ctx, "volume", "list", "-q", "--filter", "name="+volume).CombinedOutput()
	if err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}

	out, err = r.buildCmd(ctx, "volume", "rm", volume).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}

	return nil
}

func (r *DockerHelper) Stop(ctx context.Context, id string) error {
	out, err := r.buildCmd(ctx, "stop", id).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}

	return nil
}

// PullOptions configures an image pull.
type PullOptions struct {
	Image    string
	Platform string
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
}

func (r *DockerHelper) Pull(ctx context.Context, opts PullOptions) error {
	args := []string{"pull"}
	if opts.Platform != "" {
		args = append(args, "--platform", opts.Platform)
	}
	args = append(args, opts.Image)
	cmd := r.buildCmd(ctx, args...)
	cmd.Stdin = opts.Stdin
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	return cmd.Run()
}

func (r *DockerHelper) Remove(ctx context.Context, id string) error {
	out, err := r.buildCmd(ctx, "rm", id).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", string(out), err)
	}

	return nil
}

func (r *DockerHelper) Run(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return r.RunWithDir(ctx, "", args, stdin, stdout, stderr)
}

func (r *DockerHelper) RunWithDir(
	ctx context.Context,
	dir string,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	cmd := r.buildCmd(ctx, args...)
	cmd.Dir = dir
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func (r *DockerHelper) StartContainer(ctx context.Context, containerId string) error {
	out, err := r.buildCmd(ctx, "start", containerId).CombinedOutput()
	if err != nil {
		stateErr, _ := r.buildCmd(ctx, "inspect", containerId, "--format",
			"{{.State.Error}} (exit code: {{.State.ExitCode}})").
			CombinedOutput()
		logs, _ := r.buildCmd(ctx, "logs", containerId, "--tail", "50").CombinedOutput()
		details := strings.TrimSpace(string(stateErr) + "\n" + string(logs))
		if details != "" {
			log.Errorf("container failed to start: %s", details)
		}
		return fmt.Errorf("failed to start container: %s: %w", string(out), err)
	}

	container, err := r.FindContainerByID(ctx, []string{containerId})
	if err != nil {
		return err
	} else if container == nil {
		return fmt.Errorf("container not found")
	}

	return nil
}

// WaitContainerRunning waits for the given container to be running, returning an error if
// it is in a terminal state or does not become running within a timeout.
func (r *DockerHelper) WaitContainerRunning(ctx context.Context, containerID string) error {
	var lastErr error
	start := time.Now()
	pollErr := wait.PollUntilContextTimeout(
		ctx, containerRunningPollInterval, containerRunningTimeout, true,
		func(ctx context.Context) (bool, error) {
			details, err := r.InspectContainers(ctx, []string{containerID})
			if err != nil {
				lastErr = err
				log.Debugf("WaitContainerRunning: inspect error (will retry): %v", err)
				return false, nil
			}
			lastErr = nil
			return r.evaluateContainerState(ctx, containerID, details, time.Since(start))
		},
	)
	if pollErr != nil && lastErr != nil {
		return fmt.Errorf("%w (last inspect error: %v)", pollErr, lastErr)
	}
	return pollErr
}

func (r *DockerHelper) GetImageTag(ctx context.Context, imageID string) (string, error) {
	args := []string{
		"inspect",
		"--type",
		"image",
		"--format",
		"{{if .RepoTags}}{{index .RepoTags 0}}{{end}}",
	}
	args = append(args, imageID)
	out, err := r.buildCmd(ctx, args...).Output()
	if err != nil {
		return "", fmt.Errorf("inspect container: %w", command.WrapCommandError(out, err))
	}

	repoTag := string(out)
	tagSplits := strings.Split(repoTag, ":")

	if len(tagSplits) > 0 {
		return strings.TrimSpace(tagSplits[1]), nil
	}

	return "", nil
}

func (r *DockerHelper) InspectImage(
	ctx context.Context,
	imageName string,
	tryRemote bool,
) (*config.ImageDetails, error) {
	imageDetails := []*config.ImageDetails{}
	err := r.Inspect(ctx, []string{imageName}, "image", &imageDetails)
	if err != nil {
		// try remote?
		if !tryRemote {
			return nil, err
		}

		imageConfig, _, err := image.GetImageConfig(ctx, imageName)
		if err != nil {
			return nil, fmt.Errorf("get image config remotely: %w", err)
		}

		return &config.ImageDetails{
			ID: imageName,
			Config: config.ImageDetailsConfig{
				User:       imageConfig.Config.User,
				Env:        imageConfig.Config.Env,
				Labels:     imageConfig.Config.Labels,
				Entrypoint: imageConfig.Config.Entrypoint,
				Cmd:        imageConfig.Config.Cmd,
			},
		}, nil
	} else if len(imageDetails) == 0 {
		return nil, fmt.Errorf("cannot find image details for %s", imageName)
	}

	return imageDetails[0], nil
}

func (r *DockerHelper) InspectContainers(
	ctx context.Context,
	ids []string,
) ([]config.ContainerDetails, error) {
	details := []config.ContainerDetails{}
	err := r.Inspect(ctx, ids, "container", &details)
	if err != nil {
		return nil, err
	}

	return details, nil
}

func (r *DockerHelper) IsPodman() bool {
	return r.GetRuntime().Name() == RuntimePodman
}

func (r *DockerHelper) IsNerdctl() bool {
	return r.GetRuntime().Name() == RuntimeNerdctl
}

func (r *DockerHelper) Inspect(
	ctx context.Context,
	ids []string,
	inspectType string,
	obj any,
) error {
	args := []string{"inspect", "--type", inspectType}
	args = append(args, ids...)
	out, err := r.buildCmd(ctx, args...).Output()
	if err != nil {
		return fmt.Errorf("inspect container: %w", command.WrapCommandError(out, err))
	}

	err = json.Unmarshal(out, obj)
	if err != nil {
		return fmt.Errorf("parse inspect output: %w", err)
	}

	return nil
}

// FindContainer will try to find a container based on the input labels.
// If no container is found, it will search for the labels manually inspecting
// containers.
func (r *DockerHelper) FindContainer(ctx context.Context, labels []string) ([]string, error) {
	args := []string{"ps", "-q", "-a"}
	for _, label := range labels {
		args = append(args, "--filter", "label="+label)
	}

	out, err := r.buildCmd(ctx, args...).Output()
	if err != nil {
		// fallback to manual search
		return r.FindContainerJSON(ctx, labels)
	}

	arr := []string{}
	scan := scanner.NewScanner(bytes.NewReader(out))
	for scan.Scan() {
		arr = append(arr, strings.TrimSpace(scan.Text()))
	}

	return arr, nil
}

// FindContainerJSON will manually search for containers with matching labels.
// This is useful in case the `--filter` doesn't work.
func (r *DockerHelper) FindContainerJSON(ctx context.Context, labels []string) ([]string, error) {
	args := []string{"ps", "-q", "-a"}
	out, err := r.buildCmd(ctx, args...).Output()
	if err != nil {
		return nil, command.WrapCommandError(out, err)
	}

	result := []string{}

	ids := strings.SplitSeq(strings.TrimSuffix(string(out), "\n"), "\n")
	for id := range ids {
		id = strings.TrimSpace(id)
		found := true

		containers, err := r.InspectContainers(ctx, []string{id})
		if err != nil || len(containers) == 0 {
			continue
		}

		for _, label := range labels {
			key, value, _ := strings.Cut(label, "=")
			if containers[0].Config.Labels[key] != value {
				found = false
				break
			}
		}

		if found {
			result = append(result, id)
		}
	}

	return result, nil
}

func (r *DockerHelper) GetContainerLogs(
	ctx context.Context,
	id string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	args := []string{"logs", id}
	cmd := r.buildCmd(ctx, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	return cmd.Run()
}

// containerStateError returns an error describing the container's state, including its
// exit code and logs.
func (r *DockerHelper) containerStateError(
	ctx context.Context,
	containerID string,
	state config.ContainerDetailsState,
	sentinel error,
) error {
	detail := fmt.Sprintf("exit code %d", state.ExitCode)
	if state.Error != "" {
		detail += fmt.Sprintf(", error: %s", state.Error)
	}
	if logs := r.tailContainerLogs(ctx, containerID, 20); logs != "" {
		detail += fmt.Sprintf("\nlogs:\n%s", logs)
	}
	return fmt.Errorf(
		"%w: container %s is %q (%s)",
		sentinel,
		containerID,
		strings.ToLower(state.Status),
		detail,
	)
}

// tailContainerLogs returns the tail of the container's logs for diagnostics,
// or "" if the logs cannot be retrieved.
func (r *DockerHelper) tailContainerLogs(
	ctx context.Context,
	containerID string,
	lines int,
) string {
	out, err := r.buildCmd(ctx, "logs", containerID, "--tail", fmt.Sprintf("%d", lines)).
		CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// evaluateContainerState checks the container's state and returns true if it is running.
// If the container is in a terminal state or has exited after the grace period, it returns an error.
func (r *DockerHelper) evaluateContainerState(
	ctx context.Context,
	containerID string,
	details []config.ContainerDetails,
	elapsed time.Duration,
) (bool, error) {
	if len(details) == 0 {
		return false, fmt.Errorf(
			"container %s disappeared while waiting for it to start",
			containerID,
		)
	}
	state := details[0].State
	status := strings.ToLower(state.Status)
	if status == "running" {
		return true, nil
	}
	if sentinel := failedBootSentinel(status, elapsed > containerExitGrace); sentinel != nil {
		return false, r.containerStateError(ctx, containerID, state, sentinel)
	}
	log.Debugf("WaitContainerRunning: container %s status=%s, waiting", containerID, status)
	return false, nil
}

// failedBootSentinel returns an error if the container is in a terminal state or has exited
// after the grace period, or nil if it is still booting.
func failedBootSentinel(status string, graceElapsed bool) error {
	switch status {
	case "dead", "removing":
		return ErrContainerTerminal
	case "exited", "created":
		if graceElapsed {
			return ErrContainerExited
		}
	}
	return nil
}

func (r *DockerHelper) buildCmd(ctx context.Context, args ...string) *exec.Cmd {
	name, cmdArgs := r.DockerCommand, args
	if r.Elevator != nil {
		_ = r.EnsureElevated() // defensive; normally already done in NewDockerDriver
		name, cmdArgs = r.Elevator.wrap(r.DockerCommand, r.Environment, args)
	}
	//nolint:gosec // command and args come from trusted provider config
	cmd := exec.CommandContext(ctx, name, cmdArgs...)
	if r.Environment != nil {
		cmd.Env = append(os.Environ(), r.Environment...)
	}
	return cmd
}
