package apple

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/image"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	containerRunningPollInterval = 500 * time.Millisecond
	containerRunningTimeout      = 60 * time.Second
	systemStartTimeout           = 30 * time.Second
)

// AppleHelper shells out to Apple's `container` CLI, translating operations to
// its grouped subcommands and parsing its JSON output (it has no Go-template
// `--format`).
type AppleHelper struct {
	Command     string
	Environment []string
}

// EnsureSystemRunning starts the container system service if it is not running;
// it must be running before any container operation.
func (h *AppleHelper) EnsureSystemRunning(ctx context.Context) error {
	if h.SystemRunning(ctx) {
		return nil
	}

	cctx, cancel := context.WithTimeout(ctx, systemStartTimeout)
	defer cancel()
	out, err := h.buildCmd(cctx, "system", "start").CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"start container system service (run `container system start` manually): %s: %w",
			strings.TrimSpace(string(out)), err,
		)
	}
	return nil
}

func (h *AppleHelper) ClientVersion(ctx context.Context) string {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := h.buildCmd(cctx, "system", "version", "--format", "json").Output()
	if err != nil {
		return ""
	}
	var versions []struct {
		AppName string `json:"appName"`
		Version string `json:"version"`
	}
	if json.Unmarshal(out, &versions) != nil {
		return ""
	}
	for _, v := range versions {
		if v.AppName == "container" {
			return strings.TrimSpace(v.Version)
		}
	}
	return ""
}

type PullOptions struct {
	Image    string
	Platform string
	Stdout   io.Writer
	Stderr   io.Writer
}

func (h *AppleHelper) Pull(ctx context.Context, opts PullOptions) error {
	args := []string{"image", "pull"}
	if opts.Platform != "" {
		args = append(args, "--platform", opts.Platform)
	}
	args = append(args, opts.Image)
	cmd := h.buildCmd(ctx, args...)
	cmd.Stdout = opts.Stdout
	cmd.Stderr = opts.Stderr
	return cmd.Run()
}

func (h *AppleHelper) Push(ctx context.Context, image string, stdout, stderr io.Writer) error {
	cmd := h.buildCmd(ctx, "image", "push", image)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func (h *AppleHelper) Tag(ctx context.Context, image, tag string) error {
	out, err := h.buildCmd(ctx, "image", "tag", image, tag).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (h *AppleHelper) Stop(ctx context.Context, id string) error {
	out, err := h.buildCmd(ctx, "stop", id).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (h *AppleHelper) Remove(ctx context.Context, id string) error {
	out, err := h.buildCmd(ctx, "delete", id).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Streams bundles the optional IO for a `container` invocation.
type Streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func (h *AppleHelper) Run(ctx context.Context, args []string, s Streams) error {
	return h.RunWithDir(ctx, "", args, s)
}

func (h *AppleHelper) RunWithDir(ctx context.Context, dir string, args []string, s Streams) error {
	cmd := h.buildCmd(ctx, args...)
	cmd.Dir = dir
	cmd.Stdin = s.Stdin
	cmd.Stdout = s.Stdout
	cmd.Stderr = s.Stderr
	return cmd.Run()
}

func (h *AppleHelper) StartContainer(ctx context.Context, id string) error {
	out, err := h.buildCmd(ctx, "start", id).CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start container: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func (h *AppleHelper) WaitContainerRunning(ctx context.Context, id string) error {
	// Inspect errors are treated as transient (the container may not be
	// queryable immediately after start) and polling continues, but the last
	// one is retained so a timeout surfaces the real cause instead of a bare
	// deadline. A terminal (exited) state fails fast.
	var lastErr error
	pollErr := wait.PollUntilContextTimeout(
		ctx, containerRunningPollInterval, containerRunningTimeout, true,
		func(ctx context.Context) (bool, error) {
			details, err := h.InspectContainers(ctx, []string{id})
			if err != nil {
				lastErr = err
				return false, nil
			}
			if len(details) == 0 {
				return false, nil
			}
			switch details[0].State.Status {
			case config.ContainerStatusRunning:
				return true, nil
			case config.ContainerStatusExited:
				return false, fmt.Errorf("container %s exited before reaching running state", id)
			default:
				return false, nil
			}
		},
	)
	if pollErr != nil && lastErr != nil {
		return fmt.Errorf("%w (last inspect error: %w)", pollErr, lastErr)
	}
	return pollErr
}

func (h *AppleHelper) GetContainerLogs(
	ctx context.Context,
	id string,
	stdout, stderr io.Writer,
) error {
	cmd := h.buildCmd(ctx, "logs", id)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func (h *AppleHelper) InspectContainers(
	ctx context.Context,
	ids []string,
) ([]config.ContainerDetails, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := append([]string{"inspect"}, ids...)
	out, err := h.buildCmd(ctx, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("inspect container: %w", command.WrapCommandError(out, err))
	}

	var raw []containerInspect
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse inspect output: %w", err)
	}

	details := make([]config.ContainerDetails, 0, len(raw))
	for _, r := range raw {
		details = append(details, r.toContainerDetails())
	}
	return details, nil
}

func (h *AppleHelper) FindDevContainer(
	ctx context.Context,
	labels []string,
) (*config.ContainerDetails, error) {
	all, err := h.listContainers(ctx)
	if err != nil {
		return nil, err
	}

	for _, c := range all {
		if strings.ToLower(c.Status.State) == "removing" {
			continue
		}
		if matchesLabels(c.Configuration.Labels, labels) {
			details := c.toContainerDetails()
			return &details, nil
		}
	}
	return nil, nil
}

func (h *AppleHelper) FindContainerByID(
	ctx context.Context,
	ids []string,
) (*config.ContainerDetails, error) {
	details, err := h.InspectContainers(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range details {
		if details[i].State.Status != config.ContainerStatusRemoving {
			return &details[i], nil
		}
	}
	return nil, nil
}

func matchesLabels(containerLabels map[string]string, selectors []string) bool {
	for _, sel := range selectors {
		key, value, _ := strings.Cut(sel, "=")
		if containerLabels[key] != value {
			return false
		}
	}
	return true
}

// InspectImage falls back to the remote registry config when tryRemote is set
// and the image is not present locally.
func (h *AppleHelper) InspectImage(
	ctx context.Context,
	imageName string,
	tryRemote bool,
) (*config.ImageDetails, error) {
	out, err := h.buildCmd(ctx, "image", "inspect", imageName).Output()
	if err != nil {
		err = fmt.Errorf("inspect image %s: %w", imageName, command.WrapCommandError(out, err))
	} else {
		var raw []imageInspect
		switch {
		case json.Unmarshal(out, &raw) != nil:
			err = fmt.Errorf("parse image inspect output for %s", imageName)
		case len(raw) == 0:
			err = fmt.Errorf("no image details found for %s", imageName)
		default:
			return raw[0].toImageDetails(runtime.GOARCH), nil
		}
	}

	if !tryRemote {
		return nil, err
	}

	imageConfig, _, rerr := image.GetImageConfig(ctx, imageName)
	if rerr != nil {
		return nil, fmt.Errorf("get image config remotely: %w", rerr)
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
}

func (h *AppleHelper) GetImageTag(ctx context.Context, imageID string) (string, error) {
	out, err := h.buildCmd(ctx, "image", "inspect", imageID).Output()
	if err != nil {
		return "", fmt.Errorf("inspect image: %w", command.WrapCommandError(out, err))
	}
	var raw []imageInspect
	if err := json.Unmarshal(out, &raw); err != nil {
		return "", fmt.Errorf("parse image inspect output: %w", err)
	}
	if len(raw) == 0 {
		return "", nil
	}
	return parseImageTag(raw[0].Configuration.Name), nil
}

// parseImageTag returns the tag from an image reference. It inspects the final
// path segment (so a registry port like localhost:5000/alpine:3.20 is not
// mistaken for the tag) and drops any @digest suffix (which is not a tag).
func parseImageTag(ref string) string {
	lastSegment := ref[strings.LastIndex(ref, "/")+1:]
	lastSegment, _, _ = strings.Cut(lastSegment, "@")
	_, tag, found := strings.Cut(lastSegment, ":")
	if !found {
		return ""
	}
	return strings.TrimSpace(tag)
}

// EnsureBuilderRunning starts the BuildKit builder; an already-running builder is
// a no-op, other failures are returned so a build never runs against a dead builder.
func (h *AppleHelper) EnsureBuilderRunning(ctx context.Context) error {
	out, err := h.buildCmd(ctx, "builder", "start").CombinedOutput()
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(string(out)), "already running") {
		return nil
	}
	return fmt.Errorf("start container builder: %s: %w", strings.TrimSpace(string(out)), err)
}

// SystemRunning reports whether the container system service is running.
func (h *AppleHelper) SystemRunning(ctx context.Context) bool {
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := h.buildCmd(cctx, "system", "status").CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(out)), string(config.ContainerStatusRunning))
}

func (h *AppleHelper) buildCmd(ctx context.Context, args ...string) *exec.Cmd {
	//nolint:gosec // G204: operator-configured binary, internally-built args (as in pkg/docker)
	cmd := exec.CommandContext(ctx, h.Command, args...)
	if h.Environment != nil {
		cmd.Env = append(os.Environ(), h.Environment...)
	}
	return cmd
}

func (h *AppleHelper) listContainers(ctx context.Context) ([]containerInspect, error) {
	out, err := h.buildCmd(ctx, "list", "--all", "--format", "json").Output()
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", command.WrapCommandError(out, err))
	}
	var raw []containerInspect
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse list output: %w", err)
	}
	return raw, nil
}
