package workspace

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/devsy-org/devsy/pkg/apple"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

const defaultContainerCommand = "container"

// NewContainerRuntime returns an AppleRuntime for the apple driver, else a
// DockerRuntime. A provider-config load failure is returned rather than silently
// falling back to the (wrong) Docker runtime for an apple workspace.
func NewContainerRuntime(
	workspace *provider2.Workspace,
	override string,
) (ContainerRuntime, error) {
	isApple, err := isAppleDriver(workspace)
	if err != nil {
		return nil, err
	}
	if isApple {
		return NewAppleRuntime(workspace, override), nil
	}
	return NewDockerRuntime(workspace, override), nil
}

func isAppleDriver(workspace *provider2.Workspace) (bool, error) {
	if workspace == nil || workspace.Context == "" {
		return false, nil
	}
	providerConfig, err := provider2.LoadProviderConfig(workspace.Context, workspace.Provider.Name)
	if err != nil {
		return false, fmt.Errorf("load provider config for runtime selection: %w", err)
	}
	return providerConfig.Agent.Driver == provider2.AppleDriver, nil
}

// ResolveContainerCommand resolves the `container` binary: override, then
// agent.apple.path, then the default.
func ResolveContainerCommand(workspace *provider2.Workspace, override string) string {
	if override != "" {
		return override
	}
	if workspace == nil || workspace.Context == "" {
		return defaultContainerCommand
	}
	providerConfig, err := provider2.LoadProviderConfig(workspace.Context, workspace.Provider.Name)
	if err != nil {
		log.Warnf(
			"failed to load provider config, using default container command (ignoring agent.apple.path): %v",
			err,
		)
		return defaultContainerCommand
	}
	if providerConfig.Agent.Apple.Path != "" {
		if expanded := expandWithOptions(
			providerConfig.Agent.Apple.Path,
			workspace.Provider.Options,
		); expanded != "" {
			return expanded
		}
	}
	return defaultContainerCommand
}

// resolveContainerEnv returns agent.apple.env so exec/logs match the `up` path.
func resolveContainerEnv(workspace *provider2.Workspace) []string {
	if workspace == nil || workspace.Context == "" {
		return nil
	}
	providerConfig, err := provider2.LoadProviderConfig(workspace.Context, workspace.Provider.Name)
	if err != nil {
		return nil
	}
	var env []string
	for k, v := range providerConfig.Agent.Apple.Env {
		env = append(env, k+"="+expandWithOptions(v, workspace.Provider.Options))
	}
	return env
}

type AppleRuntime struct {
	helper *apple.AppleHelper
}

func NewAppleRuntime(workspace *provider2.Workspace, override string) *AppleRuntime {
	return &AppleRuntime{
		helper: &apple.AppleHelper{
			Command:     ResolveContainerCommand(workspace, override),
			Environment: resolveContainerEnv(workspace),
		},
	}
}

func (r *AppleRuntime) Command() string { return r.helper.Command }

func (r *AppleRuntime) Environment() []string { return r.helper.Environment }

func (r *AppleRuntime) FindRunning(
	ctx context.Context,
	workspaceID string,
	idLabels []string,
) (*devcconfig.ContainerDetails, error) {
	labels := devcconfig.GetIDLabels(workspaceID, idLabels)
	container, err := r.helper.FindDevContainer(ctx, labels)
	if err != nil {
		return nil, fmt.Errorf("find container: %w", err)
	}
	if container == nil {
		return nil, fmt.Errorf("no running container found for workspace %q", workspaceID)
	}
	if !strings.EqualFold(container.State.Status, ContainerStatusRunning) {
		return nil, fmt.Errorf(
			"container %s is not running (status: %s)",
			container.ID, container.State.Status,
		)
	}
	return container, nil
}

func (r *AppleRuntime) Exec(ctx context.Context, req ExecRequest) (int, error) {
	return execWithRunner(ctx, req, r.runner())
}

func (r *AppleRuntime) ProbeEnv(
	ctx context.Context,
	target ContainerTarget,
	probe string,
) map[string]string {
	return probeEnvWithRunner(ctx, target, probe, r.runner())
}

// runner adapts the helper's Streams-based Run to containerRunFunc.
func (r *AppleRuntime) runner() containerRunFunc {
	return func(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
		return r.helper.Run(ctx, args, apple.Streams{Stdin: stdin, Stdout: stdout, Stderr: stderr})
	}
}
