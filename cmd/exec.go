package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

const defaultDockerCommand = "docker"

type ExecCmd struct {
	*flags.GlobalFlags

	WorkspaceFolder     string
	RemoteEnv           []string
	DefaultUserEnvProbe string
}

func NewExecCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &ExecCmd{GlobalFlags: f}
	execCmd := &cobra.Command{
		Use:   "exec --workspace-folder <path> -- <cmd> [args...]",
		Short: "Executes a command in a running workspace container",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			ctx := cobraCmd.Context()
			return cmd.Run(ctx, args)
		},
	}

	execCmd.Flags().
		StringVar(
			&cmd.WorkspaceFolder,
			"workspace-folder",
			"",
			"Path to the workspace folder",
		)
	_ = execCmd.MarkFlagRequired("workspace-folder")
	execCmd.Flags().
		StringSliceVar(
			&cmd.RemoteEnv,
			"remote-env",
			[]string{},
			"Environment variables to set in the container (KEY=VALUE format)",
		)
	execCmd.Flags().
		StringVar(
			&cmd.DefaultUserEnvProbe,
			"default-user-env-probe",
			"",
			"Override userEnvProbe from config (loginInteractiveShell, loginShell, interactiveShell, none)",
		)

	return execCmd
}

func (cmd *ExecCmd) Run(ctx context.Context, args []string) error {
	if err := cmd.validateRemoteEnv(); err != nil {
		return err
	}

	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	client, err := workspace2.Get(ctx, workspace2.GetOptions{
		DevsyConfig: devsyConfig,
		Args:        []string{cmd.WorkspaceFolder},
		Owner:       cmd.Owner,
	})
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}

	workspaceConfig := client.WorkspaceConfig()
	dockerCommand := resolveDockerCommand(workspaceConfig)

	containerDetails, err := findRunningContainer(
		ctx, dockerCommand, devcontainer.GetRunnerIDFromWorkspace(workspaceConfig),
	)
	if err != nil {
		return err
	}

	result := loadExecResult(workspaceConfig, containerDetails)
	workdir := resolveExecWorkdir(result, client.Workspace())
	user := devcconfig.GetRemoteUser(result)
	userEnvProbe := resolveUserEnvProbe(result, cmd.DefaultUserEnvProbe)

	target := containerTarget{
		helper:      &docker.DockerHelper{DockerCommand: dockerCommand},
		containerID: containerDetails.ID,
		user:        user,
	}
	probedEnv := probeContainerEnv(ctx, target, userEnvProbe)
	envMap := buildExecEnv(result, cmd.RemoteEnv, probedEnv)

	return cmd.execInContainer(ctx, execOpts{
		target:  target,
		workdir: workdir,
		envMap:  envMap,
	}, args)
}

func (cmd *ExecCmd) validateRemoteEnv() error {
	for _, env := range cmd.RemoteEnv {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return fmt.Errorf("invalid remote-env value %q: must be KEY=VALUE format", env)
		}
	}
	return nil
}

func resolveDockerCommand(
	workspace *provider2.Workspace,
) string {
	if workspace == nil || workspace.Context == "" {
		return defaultDockerCommand
	}

	providerConfig, err := provider2.LoadProviderConfig(
		workspace.Context,
		workspace.Provider.Name,
	)
	if err != nil {
		log.Debugf("Failed to load provider config, defaulting to 'docker': %v", err)
		return defaultDockerCommand
	}

	if providerConfig.Agent.Docker.Path != "" {
		if expanded := os.ExpandEnv(providerConfig.Agent.Docker.Path); expanded != "" {
			return expanded
		}
	}

	return defaultDockerCommand
}

func findRunningContainer(
	ctx context.Context,
	dockerCommand string,
	workspaceID string,
) (*devcconfig.ContainerDetails, error) {
	dockerHelper := &docker.DockerHelper{
		DockerCommand: dockerCommand,
	}

	labels := devcconfig.GetDockerLabelForID(workspaceID)
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

	if strings.ToLower(container.State.Status) != "running" {
		return nil, fmt.Errorf(
			"container %s is not running (status: %s)",
			container.ID,
			container.State.Status,
		)
	}

	return container, nil
}

func loadExecResult(
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

func resolveUserEnvProbe(result *devcconfig.Result, cliOverride string) string {
	if cliOverride != "" {
		return cliOverride
	}
	if result != nil && result.MergedConfig != nil {
		return result.MergedConfig.UserEnvProbe
	}
	return ""
}

func resolveExecWorkdir(result *devcconfig.Result, workspaceName string) string {
	if result != nil && result.MergedConfig != nil && result.MergedConfig.WorkspaceFolder != "" {
		return result.MergedConfig.WorkspaceFolder
	}
	return path.Join("/workspaces", workspaceName)
}

func buildExecEnv(
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

type execOpts struct {
	target  containerTarget
	workdir string
	envMap  map[string]string
}

func (cmd *ExecCmd) execInContainer(ctx context.Context, opts execOpts, args []string) error {
	execArgs := []string{"exec", "-i"}
	if term.IsTerminal(int(os.Stdin.Fd())) { // #nosec G115 -- fd is always a valid file descriptor
		execArgs = append(execArgs, "-t")
	}
	for k, v := range opts.envMap {
		execArgs = append(execArgs, "-e", k+"="+v)
	}
	if opts.workdir != "" {
		execArgs = append(execArgs, "--workdir", opts.workdir)
	}
	if opts.target.user != "" {
		execArgs = append(execArgs, "--user", opts.target.user)
	}
	execArgs = append(execArgs, opts.target.containerID)
	execArgs = append(execArgs, args...)

	redacted := strings.Join(redactExecArgs(execArgs), " ")
	log.Debugf("Executing in container: %s %s", opts.target.helper.DockerCommand, redacted)
	return opts.target.helper.Run(ctx, execArgs, os.Stdin, os.Stdout, os.Stderr)
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

type containerTarget struct {
	helper      *docker.DockerHelper
	containerID string
	user        string
}

func probeContainerEnv(
	ctx context.Context,
	target containerTarget,
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
	target containerTarget,
	shellFlag string,
) ([]byte, byte, error) {
	args := buildProbeArgs(target, shellFlag, "cat /proc/self/environ")
	var stdout bytes.Buffer
	err := target.helper.Run(ctx, args, nil, &stdout, io.Discard)
	if err == nil {
		return stdout.Bytes(), 0, nil
	}

	log.Debugf("Env probe with /proc/self/environ failed: %v, trying printenv", err)
	args = buildProbeArgs(target, shellFlag, "printenv")
	stdout.Reset()
	err = target.helper.Run(ctx, args, nil, &stdout, io.Discard)
	if err != nil {
		return nil, 0, fmt.Errorf("probe user env: %w", err)
	}
	return stdout.Bytes(), '\n', nil
}

func buildProbeArgs(target containerTarget, shellFlag string, cmd string) []string {
	args := []string{"exec"}
	if target.user != "" {
		args = append(args, "--user", target.user)
	}
	args = append(args, target.containerID, "sh", shellFlag, cmd)
	return args
}

func redactExecArgs(args []string) []string {
	redacted := make([]string, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "-e" && i+1 < len(args) {
			redacted[i] = args[i]
			i++
			if k, _, ok := strings.Cut(args[i], "="); ok {
				redacted[i] = k + "=<redacted>"
			} else {
				redacted[i] = args[i]
			}
		} else {
			redacted[i] = args[i]
		}
	}
	return redacted
}
