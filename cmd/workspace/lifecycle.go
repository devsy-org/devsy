package workspace

import (
	"context"
	"fmt"
	"os"
	"sort"

	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/types"
)

// DockerExecSubcommand is the docker subcommand used to exec into a container.
const DockerExecSubcommand = "exec"

// DockerExecArgs collects the inputs for BuildDockerExecArgs.
// User is optional; when set the lifecycle hook runs as that user, per
// the devcontainer spec (remoteUser). Without it, root-default exec cannot
// overwrite files previously created by the remoteUser on storage backends
// that do not honour CAP_DAC_OVERRIDE across UIDs.
// See https://containers.dev/implementors/json_reference/ (lifecycle-scripts).
type DockerExecArgs struct {
	Container       string
	User            string
	EnvArgs         []string
	WorkspaceFolder string
	Command         []string
}

// BuildDockerExecArgs assembles the argv passed to `docker exec`.
func BuildDockerExecArgs(a DockerExecArgs) []string {
	args := []string{DockerExecSubcommand}
	args = append(args, a.EnvArgs...)
	if a.WorkspaceFolder != "" {
		args = append(args, "--workdir", a.WorkspaceFolder)
	}
	if a.User != "" {
		args = append(args, "--user", a.User)
	}
	args = append(args, a.Container)
	if len(a.Command) == 1 {
		args = append(args, "sh", "-c", a.Command[0])
	} else {
		args = append(args, a.Command...)
	}
	return args
}

// LifecycleExecParams collects the inputs for ExecLifecycleHook.
type LifecycleExecParams struct {
	Ctx         context.Context
	Helper      *docker.DockerHelper
	ContainerID string
	EnvArgs     []string
	Workdir     string
	User        string
}

// ExecLifecycleHook runs a single devcontainer lifecycle hook against a container.
func ExecLifecycleHook(params *LifecycleExecParams, name string, hook types.LifecycleHook) error {
	if len(hook) == 0 {
		return nil
	}

	for key, command := range hook {
		if len(command) == 0 {
			continue
		}
		log.Infof("running %s: %s %v", name, key, command)

		args := BuildDockerExecArgs(DockerExecArgs{
			Container:       params.ContainerID,
			User:            params.User,
			EnvArgs:         params.EnvArgs,
			WorkspaceFolder: params.Workdir,
			Command:         command,
		})
		if err := params.Helper.Run(params.Ctx, args, os.Stdin, os.Stdout, os.Stderr); err != nil {
			return fmt.Errorf("command %q failed: %w", key, err)
		}
	}

	return nil
}

// BuildLifecycleEnvArgs converts a merged config's RemoteEnv into -e KEY=VALUE
// pairs suitable for `docker exec`.
func BuildLifecycleEnvArgs(result *devcconfig.Result) []string {
	if result == nil || result.MergedConfig == nil {
		return nil
	}

	env := result.MergedConfig.RemoteEnv
	if len(env) == 0 {
		return nil
	}

	keys := make([]string, 0, len(env))
	for k, v := range env {
		if v != nil {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	args := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		args = append(args, "-e", k+"="+*env[k])
	}
	return args
}
