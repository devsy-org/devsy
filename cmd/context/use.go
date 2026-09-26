package context

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/spf13/cobra"
)

// UseCmd holds the use cmd flags.
type UseCmd struct {
	*flags.GlobalFlags

	Options []string
}

// NewUseCmd creates a new command.
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

	cliflags.Add(
		useCmd,
		cliflags.StringArray(&cmd.Options, names.Option, []string{}, "context option in the form KEY=VALUE").
			Shorthand("o"),
	)
	return useCmd
}

// Run runs the command logic.
func (cmd *UseCmd) Run(ctx context.Context, context string) error {
	return config.UpdateConfig("", cmd.Provider, func(devsyConfig *config.Config) error {
		if devsyConfig.Contexts[context] == nil {
			return fmt.Errorf("context %q doesn't exist", context)
		}

		if len(cmd.Options) > 0 {
			if err := setOptions(devsyConfig, context, cmd.Options); err != nil {
				return err
			}
		}

		devsyConfig.DefaultContext = context
		return nil
	})
}
