package machine

import (
	"context"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/devsy-org/log"
	"github.com/spf13/cobra"
)

// StartCmd holds the configuration.
type StartCmd struct {
	*flags.GlobalFlags
}

// NewStartCmd creates a new start command.
func NewStartCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &StartCmd{
		GlobalFlags: flags,
	}
	startCmd := &cobra.Command{
		Use:   "start [name]",
		Short: "Starts an existing machine",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	return startCmd
}

// Run runs the command logic.
func (cmd *StartCmd) Run(ctx context.Context, args []string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	machineClient, err := workspace.GetMachine(devsyConfig, args, log.Default)
	if err != nil {
		return err
	}

	err = machineClient.Start(ctx)
	if err != nil {
		return err
	}

	return nil
}
