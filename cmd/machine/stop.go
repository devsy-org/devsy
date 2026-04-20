package machine

import (
	"context"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/workspace"
	oldlog "github.com/devsy-org/log"
	"github.com/spf13/cobra"
)

// StopCmd holds the configuration.
type StopCmd struct {
	*flags.GlobalFlags
}

// NewStopCmd creates a new stop command.
func NewStopCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &StopCmd{
		GlobalFlags: flags,
	}
	stopCmd := &cobra.Command{
		Use:   "stop [name]",
		Short: "Stops an existing machine",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	return stopCmd
}

// Run runs the command logic.
func (cmd *StopCmd) Run(ctx context.Context, args []string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	machineClient, err := workspace.GetMachine(devsyConfig, args, oldlog.Default)
	if err != nil {
		return err
	}

	err = machineClient.Stop(ctx, client.StopOptions{})
	if err != nil {
		return err
	}

	return nil
}
