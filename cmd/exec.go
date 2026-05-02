package cmd

import (
	"context"
	"fmt"
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

// ExecCmd holds the exec cmd flags.
type ExecCmd struct {
	*flags.GlobalFlags

	WorkspaceFolder string
	RemoteEnv       []string
}

// NewExecCmd creates a new exec command.
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

	return execCmd
}

// Run executes the exec command.
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

	var result *devcconfig.Result
	if workspaceConfig != nil && workspaceConfig.Context != "" && workspaceConfig.ID != "" {
		result, err = provider2.LoadWorkspaceResult(workspaceConfig.Context, workspaceConfig.ID)
		if err != nil {
			log.Warnf("Error loading workspace result: %v", err)
		}
	}
	if result != nil {
		result.ContainerDetails = containerDetails
	}

	workdir := resolveExecWorkdir(result, client.Workspace())
	user := devcconfig.GetRemoteUser(result)

	return cmd.execInContainer(ctx, dockerCommand, containerDetails.ID, workdir, user, args)
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

func resolveExecWorkdir(result *devcconfig.Result, workspaceName string) string {
	if result != nil && result.MergedConfig != nil && result.MergedConfig.WorkspaceFolder != "" {
		return result.MergedConfig.WorkspaceFolder
	}
	return path.Join("/workspaces", workspaceName)
}

func (cmd *ExecCmd) execInContainer(
	ctx context.Context,
	dockerCommand string,
	containerID string,
	workdir string,
	user string,
	args []string,
) error {
	dockerHelper := &docker.DockerHelper{
		DockerCommand: dockerCommand,
	}

	execArgs := []string{"exec", "-i"}
	if term.IsTerminal(int(os.Stdin.Fd())) { // #nosec G115 -- fd is always a valid file descriptor
		execArgs = append(execArgs, "-t")
	}
	for _, env := range cmd.RemoteEnv {
		execArgs = append(execArgs, "-e", env)
	}
	if workdir != "" {
		execArgs = append(execArgs, "--workdir", workdir)
	}
	if user != "" {
		execArgs = append(execArgs, "--user", user)
	}
	execArgs = append(execArgs, containerID)
	execArgs = append(execArgs, args...)

	redacted := strings.Join(redactExecArgs(execArgs), " ")
	log.Debugf("Executing in container: %s %s", dockerCommand, redacted)
	return dockerHelper.Run(ctx, execArgs, os.Stdin, os.Stdout, os.Stderr)
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
