package workspace

import (
	"bytes"
	"maps"
	"os"
	"path"
	"strings"

	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

const (
	DefaultDockerCommand   = "docker"
	ContainerStatusRunning = "running"
)

// ResolveDockerCommand returns the docker binary to invoke. Precedence:
// caller-supplied override → provider config (agent.docker.path) → default.
// Callers wired to a --docker-path style flag pass it as override; the override
// is honored even when workspace is nil so flag handling works at every call site.
func ResolveDockerCommand(
	workspace *provider2.Workspace,
	override string,
) string {
	if override != "" {
		return override
	}
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

// probeShellFlag returns the shell flag for the given probe mode.
// Kept as a package-level helper so DockerRuntime can use it.
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

// buildProbeArgs constructs the docker exec arguments for env probing.
// Kept as a package-level helper so DockerRuntime can use it.
func buildProbeArgs(target ContainerTarget, shellFlag string, cmd string) []string {
	args := []string{"exec"}
	if target.User != "" {
		args = append(args, "--user", target.User)
	}
	args = append(args, target.ContainerID, "sh", shellFlag, cmd)
	return args
}

// parseEnvOutput parses the output of an env probe command.
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
