package machine

import (
	"context"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/workspace"
	oldlog "github.com/devsy-org/log"
	"github.com/spf13/cobra"
)

// CreateCmd holds the configuration.
type CreateCmd struct {
	*flags.GlobalFlags

	ProviderOptions []string
}

// NewCreateCmd creates a new create command.
func NewCreateCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &CreateCmd{
		GlobalFlags: flags,
	}
	createCmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Creates a new machine",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}
	createCmd.Flags().
		StringSliceVar(&cmd.ProviderOptions, "provider-option", []string{}, "Provider option in the form KEY=VALUE")
	return createCmd
}

// Run runs the command logic.
func (cmd *CreateCmd) Run(ctx context.Context, args []string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	machineClient, err := workspace.ResolveMachine(
		devsyConfig,
		args,
		cmd.ProviderOptions,
		oldlog.Default,
	)
	if err != nil {
		return err
	}

	err = machineClient.Create(ctx)
	if err != nil {
		return err
	}

	return nil
}
