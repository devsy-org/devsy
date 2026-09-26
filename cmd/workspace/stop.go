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
	"github.com/devsy-org/devsy/pkg/ide/opener"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/status"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// StopCmd holds the destroy cmd flags.
type StopCmd struct {
	*flags.GlobalFlags
	client2.StopOptions
	// Test seam; see task.Store.SetKillProcessForTest. nil resolves the
	// default store.
	quiesceUpTasks func(workspaceID string) error
}

// NewStopCmd creates a new destroy command.
func NewStopCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &StopCmd{
		GlobalFlags: flags,
	}
	stopCmd := &cobra.Command{
		Use:   "stop [flags] [workspace-path|workspace-name]",
		Short: "Stop a workspace",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.runArgs(cobraCmd.Context(), args)
		},
		ValidArgsFunction: func(rootCmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

	return stopCmd
}

// Run runs the command logic.
func (cmd *StopCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
) error {
	reporter, err := newWorkspaceStatusReporter(
		cmd.ResultFormat,
		os.Stdout,
		cmd.Verbosity > 0 || cmd.Debug,
	)
	if err != nil {
		return err
	}
	reporter = withWorkspaceJournal(reporter, client.Workspace())
	return status.Run(
		ctx,
		reporter,
		status.Operation{Phase: status.PhaseStoppingWorkspace},
		func(ctx context.Context) error {
			return cmd.run(status.WithReporter(ctx, reporter), devsyConfig, client)
		},
	)
}

func (cmd *StopCmd) runArgs(ctx context.Context, args []string) error {
	reporter, err := newWorkspaceStatusReporter(
		cmd.ResultFormat, os.Stdout, cmd.Verbosity > 0 || cmd.Debug,
	)
	if err != nil {
		return err
	}
	ctx = status.WithReporter(ctx, reporter)
	var devsyConfig *config.Config
	var client client2.BaseWorkspaceClient
	err = status.RunStep(
		ctx, status.PhaseStoppingWorkspace, "Loading workspace",
		func(ctx context.Context) error {
			var err error
			devsyConfig, err = config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}
			if err := clientimplementation.DecodePlatformOptionsFromEnv(
				&cmd.Platform,
			); err != nil {
				return fmt.Errorf("decode platform options: %w", err)
			}
			client, err = workspace2.Get(ctx, workspace2.GetOptions{
				DevsyConfig: devsyConfig, Args: args, Owner: cmd.Owner,
			})
			return err
		},
	)
	if err != nil {
		return err
	}
	reporter = withWorkspaceJournal(reporter, client.Workspace())
	return status.Run(
		ctx,
		reporter,
		status.Operation{Phase: status.PhaseStoppingWorkspace},
		func(ctx context.Context) error {
			return cmd.run(status.WithReporter(ctx, reporter), devsyConfig, client)
		},
	)
}

func (cmd *StopCmd) run(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
) error {
	// Quiesce before waiting for the workspace lock: a still-provisioning up
	// worker can be the one holding it.
	if err := cmd.quiesce(client.Workspace()); err != nil {
		return err
	}

	// lock workspace
	if !cmd.Platform.Enabled {
		err := status.RunStep(
			ctx,
			status.PhaseStoppingWorkspace,
			"Waiting for workspace lock",
			client.Lock,
		)
		if err != nil {
			return err
		}
		defer client.Unlock()
	}

	// Quiesce again under the lock: a task may have become visible while
	// the lock wait blocked, and a surviving up worker can restart the
	// workspace after the stop.
	if err := cmd.quiesce(client.Workspace()); err != nil {
		return err
	}

	// get instance status
	var instanceStatus client2.Status
	err := status.RunStep(
		ctx,
		status.PhaseStoppingWorkspace,
		"Checking workspace status",
		func(ctx context.Context) error {
			var err error
			instanceStatus, err = client.Status(ctx, client2.StatusOptions{})
			return err
		},
	)
	if err != nil {
		return err
	} else if instanceStatus != client2.StatusRunning {
		return fmt.Errorf("cannot stop workspace because it is %q", instanceStatus)
	}

	// stop if single machine provider
	var wasStopped bool
	err = status.RunStep(
		ctx,
		status.PhaseStoppingWorkspace,
		"Checking shared machine",
		func(ctx context.Context) error {
			var err error
			wasStopped, err = cmd.stopSingleMachine(ctx, client, devsyConfig)
			return err
		},
	)
	if err != nil {
		return err
	} else if wasStopped {
		opener.KillBrowserTunnel(client.Context(), client.Workspace())
		return nil
	}

	// stop environment
	err = status.RunStep(
		ctx,
		status.PhaseStoppingWorkspace,
		"Stopping workspace resources",
		func(ctx context.Context) error {
			return client.Stop(ctx, client2.StopOptions{})
		},
	)
	if err != nil {
		return err
	}

	opener.KillBrowserTunnel(client.Context(), client.Workspace())

	return nil
}

func (cmd *StopCmd) stopSingleMachine(
	ctx context.Context,
	client client2.BaseWorkspaceClient,
	devsyConfig *config.Config,
) (bool, error) {
	// check if single machine
	singleMachineName := workspace2.SingleMachineName(
		devsyConfig,
		client.Provider(),
	)
	if !devsyConfig.Current().IsSingleMachine(client.Provider()) ||
		client.WorkspaceConfig().Machine.ID != singleMachineName {
		return false, nil
	}

	// try to find other workspace with same machine
	workspaces, err := workspace2.List(ctx, devsyConfig, false, cmd.Owner)
	if err != nil {
		return false, fmt.Errorf("list workspaces: %w", err)
	}

	if otherWorkspaceUsesMachine(workspaces, client.Workspace(), singleMachineName) {
		return false, nil
	}

	// if no other workspace was found on this machine, delete the whole machine
	machineClient, err := workspace2.GetMachine(
		devsyConfig,
		[]string{singleMachineName},
	)
	if err != nil {
		return false, fmt.Errorf("get machine: %w", err)
	}

	// stop the machine
	err = status.RunStep(
		ctx,
		status.PhaseStoppingWorkspace,
		"Stopping machine",
		func(ctx context.Context) error {
			return machineClient.Stop(ctx, client2.StopOptions{})
		},
	)
	if err != nil {
		return false, fmt.Errorf("stop machine: %w", err)
	}

	log.Debugf("stopped workspace: workspace=%s", client.Workspace())
	return true, nil
}

func otherWorkspaceUsesMachine(
	workspaces []*provider.Workspace,
	currentWorkspaceID, machineName string,
) bool {
	for _, ws := range workspaces {
		if ws.ID == currentWorkspaceID || ws.Machine.ID != machineName {
			continue
		}
		return true
	}
	return false
}

func (cmd *StopCmd) quiesce(workspaceID string) error {
	quiesce := cmd.quiesceUpTasks
	if quiesce == nil {
		quiesce = workspace2.QuiesceUpTasksForWorkspace
	}
	if err := quiesce(workspaceID); err != nil {
		return fmt.Errorf(
			"cancel detached up tasks for workspace %s: %w",
			workspaceID,
			err,
		)
	}
	return nil
}
