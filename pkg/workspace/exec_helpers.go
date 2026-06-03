package workspace

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"strings"

	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

const (
	DefaultDockerCommand   = "docker"
	ContainerStatusRunning = "running"
)

// ContainerTarget bundles the docker helper, container ID, and user for exec operations.
type ContainerTarget struct {
	Helper      *docker.DockerHelper
	ContainerID string
	User        string
}

func ResolveDockerCommand(
	workspace *provider2.Workspace,
) string {
	if workspace == nil || workspace.Context == "" {
		return DefaultDockerCommand
	}

	providerConfig, err := provider2.LoadProviderConfig(
		workspace.Context,
		workspace.Provider.Name,
	)
	if err != nil {
		log.Debugf("Failed to load provider config, defaulting to 'docker': %v", err)
		return DefaultDockerCommand
	}

	if providerConfig.Agent.Docker.Path != "" {
		if expanded := os.ExpandEnv(providerConfig.Agent.Docker.Path); expanded != "" {
			return expanded
		}
	}

	return DefaultDockerCommand
}

func FindRunningContainer(
	ctx context.Context,
	dockerCommand string,
	workspaceID string,
	idLabels []string,
) (*devcconfig.ContainerDetails, error) {
	dockerHelper := &docker.DockerHelper{
		DockerCommand: dockerCommand,
	}

	labels := devcconfig.GetIDLabels(workspaceID, idLabels)
	container, err := dockerHelper.FindDevContainer(ctx, labels)
	if err != nil {
		return nil, fmt.Errorf("find container: %w", err)
	}
	if container == nil {
		return nil, fmt.Errorf(
			"no running container found for workspace %q",
			workspaceID,
		)
	}

	if !strings.EqualFold(container.State.Status, ContainerStatusRunning) {
		return nil, fmt.Errorf(
			"container %s is not running (status: %s)",
			container.ID,
			container.State.Status,
		)
	}

	return container, nil
}

func LoadExecResult(
	workspaceConfig *provider2.Workspace,
	containerDetails *devcconfig.ContainerDetails,
) *devcconfig.Result {
	if workspaceConfig == nil || workspaceConfig.Context == "" || workspaceConfig.ID == "" {
		return nil
	}

	result, err := provider2.LoadWorkspaceResult(workspaceConfig.Context, workspaceConfig.ID)
	if err != nil {
		log.Warnf("Error loading workspace result: %v", err)
		return nil
	}
	if result != nil {
		result.ContainerDetails = containerDetails
	}
	return result
}

func ResolveExecWorkdir(result *devcconfig.Result, workspaceName string) string {
	if result != nil && result.MergedConfig != nil && result.MergedConfig.WorkspaceFolder != "" {
		return result.MergedConfig.WorkspaceFolder
	}
	return path.Join("/workspaces", workspaceName)
}

// BuildExecEnv merges probed env, result remote env, and caller-supplied env slices.
func BuildExecEnv(
	result *devcconfig.Result,
	cliEnv []string,
	probedEnv map[string]string,
) map[string]string {
	env := make(map[string]string, len(probedEnv))
	maps.Copy(env, probedEnv)

	if result != nil {
		applyRemoteEnv(env, mergedRemoteEnv(result))
	}

	for _, e := range cliEnv {
		if k, v, ok := strings.Cut(e, "="); ok {
			env[k] = v
		}
	}

	return env
}

func mergedRemoteEnv(result *devcconfig.Result) map[string]*string {
	merged := map[string]*string{}
	if result.MergedConfig != nil {
		maps.Copy(merged, result.MergedConfig.RemoteEnv)
	}
	if result.DevContainerConfigWithPath != nil && result.DevContainerConfigWithPath.Config != nil {
		maps.Copy(merged, result.DevContainerConfigWithPath.Config.RemoteEnv)
	}
	return merged
}

func applyRemoteEnv(env map[string]string, remoteEnv map[string]*string) {
	for k, v := range remoteEnv {
		if v == nil {
			delete(env, k)
		} else {
			env[k] = *v
		}
	}
}

// ProbeContainerEnv probes the container's environment via the given probe strategy.
func ProbeContainerEnv(
	ctx context.Context,
	target ContainerTarget,
	probe string,
) map[string]string {
	userEnvProbe, err := devcconfig.NewUserEnvProbe(probe)
	if err != nil {
		log.Warnf("Invalid userEnvProbe %q, using default: %v", probe, err)
		userEnvProbe = devcconfig.DefaultUserEnvProbe
	}
	if userEnvProbe == devcconfig.NoneProbe {
		return map[string]string{}
	}

	shellFlag := probeShellFlag(userEnvProbe)

	out, sep, err := runProbeCommand(ctx, target, shellFlag)
	if err != nil {
		log.Warnf("Failed to probe user env: %v", err)
		return map[string]string{}
	}
	return parseEnvOutput(out, sep)
}

func probeShellFlag(probe devcconfig.UserEnvProbe) string {
	switch probe {
	case devcconfig.LoginInteractiveShellProbe:
		return "-lic"
	case devcconfig.LoginShellProbe:
		return "-lc"
	case devcconfig.InteractiveShellProbe:
		return "-ic"
	default:
		return "-c"
	}
}

func runProbeCommand(
	ctx context.Context,
	target ContainerTarget,
	shellFlag string,
) ([]byte, byte, error) {
	args := buildProbeArgs(target, shellFlag, "cat /proc/self/environ")
	var stdout bytes.Buffer
	err := target.Helper.Run(ctx, args, nil, &stdout, io.Discard)
	if err == nil {
		return stdout.Bytes(), 0, nil
	}

	log.Debugf("Env probe with /proc/self/environ failed: %v, trying printenv", err)
	args = buildProbeArgs(target, shellFlag, "printenv")
	stdout.Reset()
	err = target.Helper.Run(ctx, args, nil, &stdout, io.Discard)
	if err != nil {
		return nil, 0, fmt.Errorf("probe user env: %w", err)
	}
	return stdout.Bytes(), '\n', nil
}

func buildProbeArgs(target ContainerTarget, shellFlag string, cmd string) []string {
	args := []string{"exec"}
	if target.User != "" {
		args = append(args, "--user", target.User)
	}
	args = append(args, target.ContainerID, "sh", shellFlag, cmd)
	return args
}

func parseEnvOutput(out []byte, sep byte) map[string]string {
	entries := bytes.Split(out, []byte{sep})
	env := make(map[string]string, len(entries))
	for _, e := range entries {
		if len(e) == 0 {
			continue
		}
		name, value, ok := bytes.Cut(e, []byte{'='})
		if !ok || len(name) == 0 {
			continue
		}
		env[string(name)] = string(value)
	}
	delete(env, "PWD")
	return env
}
