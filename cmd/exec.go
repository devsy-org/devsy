package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/log"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// ExecCmd holds the exec cmd flags.
type ExecCmd struct {
	*flags.GlobalFlags

	WorkspaceFolder string
	Config          string
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
		StringVar(
			&cmd.Config,
			"config",
			"",
			"Path to a specific devcontainer.json",
		)
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

	containerDetails, err := findRunningContainer(ctx, client.Workspace())
	if err != nil {
		return err
	}

	return cmd.execInContainer(ctx, containerDetails.ID, args)
}

func findRunningContainer(
	ctx context.Context,
	workspaceID string,
) (*devcconfig.ContainerDetails, error) {
	dockerHelper := &docker.DockerHelper{
		DockerCommand: "docker",
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

func (cmd *ExecCmd) execInContainer(
	ctx context.Context,
	containerID string,
	args []string,
) error {
	dockerHelper := &docker.DockerHelper{
		DockerCommand: "docker",
	}

	execArgs := []string{"exec", "-i"}
	for _, env := range cmd.RemoteEnv {
		execArgs = append(execArgs, "-e", env)
	}
	execArgs = append(execArgs, containerID)
	execArgs = append(execArgs, args...)

	log.Debugf("Executing in container: docker %s", strings.Join(execArgs, " "))
	return dockerHelper.Run(ctx, execArgs, os.Stdin, os.Stdout, os.Stderr)
}
