package workspace

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
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// StopCmd holds the destroy cmd flags.
type StopCmd struct {
	*flags.GlobalFlags
	client2.StopOptions
}

// NewStopCmd creates a new destroy command.
func NewStopCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &StopCmd{
		GlobalFlags: flags,
	}
	stopCmd := &cobra.Command{
		Use:   "stop [flags] [workspace-path|workspace-name]",
		Short: "Stops an existing workspace",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			ctx := cobraCmd.Context()
			devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}

			err = clientimplementation.DecodePlatformOptionsFromEnv(&cmd.Platform)
			if err != nil {
				return fmt.Errorf("decode platform options: %w", err)
			}

			client, err := workspace2.Get(ctx, workspace2.GetOptions{
				DevsyConfig: devsyConfig,
				Args:        args,
				Owner:       cmd.Owner,
			})
			if err != nil {
				return err
			}

			return cmd.Run(ctx, devsyConfig, client)
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
	// lock workspace
	if !cmd.Platform.Enabled {
		err := client.Lock(ctx)
		if err != nil {
			return err
		}
		defer client.Unlock()
	}

	// get instance status
	instanceStatus, err := client.Status(ctx, client2.StatusOptions{})
	if err != nil {
		return err
	} else if instanceStatus != client2.StatusRunning {
		return fmt.Errorf("cannot stop workspace because it is %q", instanceStatus)
	}

	// stop if single machine provider
	wasStopped, err := cmd.stopSingleMachine(ctx, client, devsyConfig)
	if err != nil {
		return err
	} else if wasStopped {
		opener.KillBrowserTunnel(client.Context(), client.Workspace())
		return nil
	}

	// stop environment
	err = client.Stop(ctx, client2.StopOptions{})
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

	// loop workspaces
	foundOther := false
	for _, workspace := range workspaces {
		if workspace.ID == client.Workspace() || workspace.Machine.ID != singleMachineName {
			continue
		}

		foundOther = true
		break
	}
	if foundOther {
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
	err = machineClient.Stop(ctx, client2.StopOptions{})
	if err != nil {
		return false, fmt.Errorf("delete machine: %w", err)
	}

	log.Infof("stopped workspace: workspace=%s", client.Workspace())
	return true, nil
}
