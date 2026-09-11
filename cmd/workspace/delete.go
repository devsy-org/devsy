package workspace

import (
	"context"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

const (
	aliasRm   = "rm"
	aliasDown = "down"
)

// DeleteCmd holds the delete cmd flags.
type DeleteCmd struct {
	*flags.GlobalFlags
	client2.DeleteOptions
}

// NewDeleteCmd creates a new command.
func NewDeleteCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &DeleteCmd{
		GlobalFlags: globalFlags,
	}
	deleteCmd := &cobra.Command{
		Use:     "delete [flags] [workspace-path|workspace-name]",
		Aliases: []string{aliasRm, aliasDown},
		Short:   "Delete a workspace",
		Long: `Delete a workspace by path or name.

Aliases "rm" and "down" perform the same full Devsy workspace teardown.
For Docker Compose workspaces, teardown uses Docker/Podman Compose down.

Use --ignore-not-found to treat a missing workspace as success.`,
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

	cliflags.Add(deleteCmd,
		cliflags.Bool(&cmd.IgnoreNotFound, names.IgnoreNotFound, false,
			"Treat \"workspace not found\" as a successful delete"),
		cliflags.String(&cmd.GracePeriod, names.GracePeriod, "",
			"The amount of time to give the command to delete the workspace"),
		cliflags.Bool(&cmd.Force, names.Force, false,
			"Delete workspace even if it is not found remotely anymore"),
		cliflags.Bool(&cmd.RemoveVolumes, names.RemoveVolumes, false,
			"Remove named volumes associated with the workspace "+
				"(docker compose only)"),
	)
	return deleteCmd
}

// Run runs the command logic.
func (cmd *DeleteCmd) Run(cobraCmd *cobra.Command, args []string) error {
	ctx := cobraCmd.Context()
	reporter, err := newWorkspaceStatusReporter(
		cmd.ResultFormat,
		os.Stdout,
		cmd.Verbosity > 0 || cmd.Debug,
	)
	if err != nil {
		return err
	}
	var deleteErr error
	var devsyConfig *config.Config
	deleteErr = status.Run(ctx, reporter, status.Operation{Phase: status.PhaseDeletingWorkspace}, func(context.Context) error {
		var err error
		devsyConfig, err = cmd.loadConfig()
		if err != nil {
			return err
		}
		if len(args) <= 1 {
			return cmd.deleteSingle(ctx, devsyConfig, args)
		}
		return cmd.deleteMultiple(ctx, devsyConfig, args)
	})
	if devsyConfig == nil {
		return deleteErr
	}

	count, countErr := workspace.CountLocalWorkspaces(devsyConfig.DefaultContext)
	if countErr != nil {
		log.Debugf("skipping workspace count gauge: %v", countErr)
	} else {
		telemetry.FromContext(ctx).RecordWorkspaceGauge(count)
	}

	return deleteErr
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

	log.Debugf("deleted workspace %s", name)

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

		log.Debugf("deleted workspace %s", name)
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
	return workspace.Delete(ctx, workspace.DeleteOptions{
		DevsyConfig:    devsyConfig,
		Args:           args,
		IgnoreNotFound: cmd.IgnoreNotFound,
		Force:          cmd.Force,
		ClientDelete:   cmd.DeleteOptions,
		Owner:          cmd.Owner,
	})
}
