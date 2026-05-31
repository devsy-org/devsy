package context

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/spf13/cobra"
)

// UseCmd holds the use cmd flags.
type UseCmd struct {
	*flags.GlobalFlags

	Options []string
}

// NewUseCmd uses a new command.
func NewUseCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &UseCmd{
		GlobalFlags: flags,
	}
	useCmd := &cobra.Command{
		Use:   "use",
		Short: "Set a Devsy context as the default",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("specify the context to use")
			}

			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}

	useCmd.Flags().
		StringArrayVarP(&cmd.Options, "option", "o", []string{}, "context option in the form KEY=VALUE")
	return useCmd
}

// Run runs the command logic.
func (cmd *UseCmd) Run(ctx context.Context, context string) error {
	devsyConfig, err := config.LoadConfig("", cmd.Provider)
	if err != nil {
		return err
	} else if devsyConfig.Contexts[context] == nil {
		return fmt.Errorf("context %q doesn't exist", context)
	}

	// check if there are use options set
	if len(cmd.Options) > 0 {
		err = setOptions(devsyConfig, context, cmd.Options)
		if err != nil {
			return err
		}
	}

	devsyConfig.DefaultContext = context
	err = config.SaveConfig(devsyConfig)
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	return nil
}
