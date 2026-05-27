package cmd

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide/opener"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// DeleteCmd holds the delete cmd flags.
type DeleteCmd struct {
	*flags.GlobalFlags
	client2.DeleteOptions
}

// NewDeleteCmd creates a new command.
func NewDeleteCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DeleteCmd{
		GlobalFlags: flags,
	}
	deleteCmd := &cobra.Command{
		Use:   "delete [flags] [workspace-path|workspace-name]",
		Short: "Deletes an existing workspace",
		Long: `Deletes an existing workspace. You can specify the workspace by its path or name.
If the workspace is not found, you can use the --ignore-not-found flag to treat it as a successful delete.`,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd, args)
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

	deleteCmd.Flags().
		BoolVar(&cmd.IgnoreNotFound, "ignore-not-found", false, "Treat \"workspace not found\" as a successful delete")
	deleteCmd.Flags().
		StringVar(&cmd.GracePeriod, "grace-period", "", "The amount of time to give the command to delete the workspace")
	deleteCmd.Flags().
		BoolVar(&cmd.Force, "force", false, "Delete workspace even if it is not found remotely anymore")
	deleteCmd.Flags().
		BoolVar(&cmd.RemoveVolumes, "remove-volumes", false, "Remove named volumes associated with the workspace")
	return deleteCmd
}

// Run runs the command logic.
func (cmd *DeleteCmd) Run(cobraCmd *cobra.Command, args []string) error {
	devsyConfig, err := cmd.loadConfig()
	if err != nil {
		return err
	}

	ctx := cobraCmd.Context()
	if len(args) <= 1 {
		return cmd.deleteSingle(ctx, devsyConfig, args)
	}

	return cmd.deleteMultiple(ctx, devsyConfig, args)
}

func (cmd *DeleteCmd) loadConfig() (*config.Config, error) {
	_, err := clientimplementation.DecodeOptionsFromEnv(
		config.EnvFlagsDelete,
		&cmd.DeleteOptions,
	)
	if err != nil {
		return nil, fmt.Errorf("decode delete options: %w", err)
	}

	if err := clientimplementation.DecodePlatformOptionsFromEnv(&cmd.Platform); err != nil {
		return nil, fmt.Errorf("decode platform options: %w", err)
	}

	return config.LoadConfig(cmd.Context, cmd.Provider)
}

func (cmd *DeleteCmd) deleteSingle(
	ctx context.Context,
	devsyConfig *config.Config,
	args []string,
) error {
	name, err := cmd.deleteWorkspace(ctx, devsyConfig, args)
	if err != nil {
		return err
	}

	log.Infof("deleted workspace %s", name)

	return nil
}

func (cmd *DeleteCmd) deleteMultiple(
	ctx context.Context,
	devsyConfig *config.Config,
	args []string,
) error {
	var errs []error
	for _, arg := range args {
		name, err := cmd.deleteWorkspace(ctx, devsyConfig, []string{arg})
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to delete workspace %s: %w", arg, err))

			continue
		}

		log.Infof("deleted workspace %s", name)
	}

	if len(errs) > 0 {
		return fmt.Errorf(
			"%d workspace(s) failed to delete: %v",
			len(errs),
			errs,
		)
	}

	return nil
}

func (cmd *DeleteCmd) deleteWorkspace(
	ctx context.Context,
	devsyConfig *config.Config,
	args []string,
) (string, error) {
	// Best-effort: terminate any detached browser tunnel helper so its process
	// (and the host ports it holds) doesn't outlive the workspace directory.
	// If the workspace can't be resolved (already gone with --ignore-not-found,
	// for example), skip the kill — workspace.Delete is the source of truth
	// for whether the delete succeeds.
	client, err := workspace.Get(ctx, workspace.GetOptions{
		DevsyConfig: devsyConfig,
		Args:        args,
		Owner:       cmd.Owner,
	})
	if err == nil {
		opener.KillBrowserTunnel(client.Context(), client.Workspace())
	} else {
		// Without a resolved workspace the context/workspace pair the tunnel
		// state lives under is unknown, and guessing risks SIGTERMing the
		// wrong workspace's helper. This is the expected happy path under
		// --ignore-not-found when the workspace is already gone, so log at
		// Debug rather than Warn to avoid misleading operators.
		log.Debugf("resolve workspace for tunnel cleanup failed: %v", err)
	}

	return workspace.Delete(ctx, workspace.DeleteOptions{
		DevsyConfig:    devsyConfig,
		Args:           args,
		IgnoreNotFound: cmd.IgnoreNotFound,
		Force:          cmd.Force,
		ClientDelete:   cmd.DeleteOptions,
		Owner:          cmd.Owner,
	})
}
