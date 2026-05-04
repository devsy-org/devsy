package cmd

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// DownCmd holds the down cmd flags.
type DownCmd struct {
	*flags.GlobalFlags
	client2.DeleteOptions
}

// NewDownCmd creates a new down command that stops and deletes a workspace.
func NewDownCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DownCmd{
		GlobalFlags: flags,
	}
	downCmd := &cobra.Command{
		Use:   "down [flags] [workspace-path|workspace-name]",
		Short: "Stops and deletes an existing workspace",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
		ValidArgsFunction: func(
			rootCmd *cobra.Command, args []string, toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return completion.GetWorkspaceSuggestions(
				rootCmd,
				cmd.Context,
				cmd.Provider,
				args,
				toComplete,
				cmd.Owner,
			)
		},
	}

	downCmd.Flags().
		BoolVar(&cmd.Force, "force", false, "Delete workspace even if it is not found remotely anymore")
	downCmd.Flags().
		StringVar(&cmd.GracePeriod, "grace-period", "", "The amount of time to give the command to delete the workspace")
	downCmd.Flags().
		BoolVar(&cmd.RemoveVolumes, "remove-volumes", false, "Remove named volumes associated with the workspace")
	return downCmd
}

// Run stops and then deletes the workspace.
func (cmd *DownCmd) Run(ctx context.Context, args []string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	if _, err := clientimplementation.DecodeOptionsFromEnv(
		config.EnvFlagsDelete,
		&cmd.DeleteOptions,
	); err != nil {
		return fmt.Errorf("decode delete options: %w", err)
	}

	if err := clientimplementation.DecodePlatformOptionsFromEnv(&cmd.Platform); err != nil {
		return fmt.Errorf("decode platform options: %w", err)
	}

	client, err := workspace.Get(ctx, workspace.GetOptions{
		DevsyConfig: devsyConfig,
		Args:        args,
		Owner:       cmd.Owner,
	})
	if err != nil {
		return err
	}

	if err := cmd.stop(ctx, client); err != nil {
		return err
	}

	_, err = workspace.Delete(ctx, workspace.DeleteOptions{
		DevsyConfig:  devsyConfig,
		Args:         args,
		ClientDelete: cmd.DeleteOptions,
		Force:        cmd.Force,
		Owner:        cmd.Owner,
	})
	if err != nil {
		return err
	}

	log.Infof("successfully stopped and deleted workspace")
	return nil
}

func (cmd *DownCmd) stop(
	ctx context.Context,
	client client2.BaseWorkspaceClient,
) error {
	if !cmd.Platform.Enabled {
		if err := client.Lock(ctx); err != nil {
			return err
		}
		defer client.Unlock()
	}

	status, err := client.Status(ctx, client2.StatusOptions{})
	if err != nil {
		return err
	}

	if status != client2.StatusRunning {
		log.Infof("workspace is %s, skipping stop", status)
		return nil
	}

	// Single-machine stop is not needed here: workspace.Delete handles
	// single-machine cleanup via deleteSingleMachine.
	if err := client.Stop(ctx, client2.StopOptions{}); err != nil {
		return fmt.Errorf("stop workspace: %w", err)
	}

	return nil
}
