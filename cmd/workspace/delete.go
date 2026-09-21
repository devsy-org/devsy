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
	devsyConfig, err := cmd.loadConfig()
	if err != nil {
		return status.Run(ctx, reporter, status.Operation{Phase: status.PhaseDeletingWorkspace},
			func(context.Context) error { return err })
	}
	if len(args) == 0 {
		return cmd.deleteInteractively(ctx, reporter, devsyConfig)
	}
	targets := resolveDeleteTargets(ctx, devsyConfig, cmd.Owner, args)
	if len(args) <= 1 {
		reporter = withWorkspaceJournal(reporter, targets[journalWorkspaceKey(args)].ID)
	}
	deleteErr := status.Run(
		ctx,
		reporter,
		status.Operation{Phase: status.PhaseDeletingWorkspace},
		func(ctx context.Context) error {
			if len(args) <= 1 {
				return cmd.deleteSingle(
					status.WithReporter(ctx, reporter),
					devsyConfig,
					targets[journalWorkspaceKey(args)],
					args,
				)
			}
			return cmd.deleteMultiple(ctx, devsyConfig, reporter, targets, args)
		},
	)

	recordWorkspaceCount(ctx, devsyConfig)

	return deleteErr
}

// deleteInteractively resolves the deletion target with a single interactive
// selection and deletes that same client, so the operation is journaled under
// the workspace that is actually deleted.
func (cmd *DeleteCmd) deleteInteractively(
	ctx context.Context,
	reporter status.Reporter,
	devsyConfig *config.Config,
) error {
	client, err := workspace.Get(ctx, workspace.GetOptions{
		DevsyConfig: devsyConfig,
		Owner:       cmd.Owner,
	})
	if err != nil {
		return status.Run(
			ctx,
			reporter,
			status.Operation{Phase: status.PhaseDeletingWorkspace},
			func(context.Context) error { return err },
		)
	}

	deleteErr := cmd.deleteResolved(ctx, reporter, devsyConfig, client)
	recordWorkspaceCount(ctx, devsyConfig)

	return deleteErr
}

// deleteResolved deletes an already-resolved workspace client and journals
// the operation under the client's workspace ID.
func (cmd *DeleteCmd) deleteResolved(
	ctx context.Context,
	reporter status.Reporter,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
) error {
	reporter = withWorkspaceJournal(reporter, client.Workspace())
	return status.Run(
		ctx,
		reporter,
		status.Operation{Phase: status.PhaseDeletingWorkspace},
		func(ctx context.Context) error {
			_, err := cmd.deleteClient(status.WithReporter(ctx, reporter), devsyConfig, client)
			return err
		},
	)
}

func (cmd *DeleteCmd) deleteClient(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
) (string, error) {
	return workspace.Delete(ctx, workspace.DeleteOptions{
		DevsyConfig:    devsyConfig,
		Client:         client,
		IgnoreNotFound: cmd.IgnoreNotFound,
		Force:          cmd.Force,
		ClientDelete:   cmd.DeleteOptions,
		Owner:          cmd.Owner,
	})
}

// recordWorkspaceCount reports the remaining local workspace count as a gauge.
func recordWorkspaceCount(ctx context.Context, devsyConfig *config.Config) {
	count, err := workspace.CountLocalWorkspaces(devsyConfig.DefaultContext)
	if err != nil {
		log.Debugf("skipping workspace count gauge: %v", err)
		return
	}
	telemetry.FromContext(ctx).RecordWorkspaceGauge(count)
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
	target resolvedDeleteTarget,
	args []string,
) error {
	name, err := cmd.deleteTarget(ctx, devsyConfig, target, args)
	if err != nil {
		return err
	}

	log.Debugf("deleted workspace %s", name)

	return nil
}

func (cmd *DeleteCmd) deleteMultiple(
	ctx context.Context,
	devsyConfig *config.Config,
	reporter status.Reporter,
	targets map[string]resolvedDeleteTarget,
	args []string,
) error {
	var errs []error
	for _, arg := range args {
		target := targets[arg]
		targetReporter := withWorkspaceJournal(reporter, target.ID)
		name, err := cmd.deleteTarget(
			status.WithReporter(ctx, targetReporter),
			devsyConfig,
			target,
			[]string{arg},
		)
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

// deleteTarget deletes one workspace. When the target carries a pre-resolved
// client, deletion reuses it so the deleted workspace and the journal key
// cannot diverge; otherwise it resolves the raw arguments.
func (cmd *DeleteCmd) deleteTarget(
	ctx context.Context,
	devsyConfig *config.Config,
	target resolvedDeleteTarget,
	args []string,
) (string, error) {
	if target.Client != nil {
		return cmd.deleteClient(ctx, devsyConfig, target.Client)
	}
	return cmd.deleteWorkspace(ctx, devsyConfig, args)
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
