package context

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/spf13/cobra"
)

// SetOptionsCmd holds the setOptions cmd flags.
type SetOptionsCmd struct {
	*flags.GlobalFlags

	Options []string
}

// NewSetOptionsCmd setOptionss a new command.
func NewSetOptionsCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &SetOptionsCmd{
		GlobalFlags: flags,
	}
	setOptionsCmd := &cobra.Command{
		Use:   "set",
		Short: "Set options for a Devsy context",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return fmt.Errorf("please specify the context")
			}

			devsyContext := ""
			if len(args) == 1 {
				devsyContext = args[0]
			}

			return cmd.Run(cobraCmd.Context(), devsyContext)
		},
	}

	setOptionsCmd.Flags().
		StringArrayVarP(&cmd.Options, "option", "o", []string{}, "context option in the form KEY=VALUE")
	return setOptionsCmd
}

// Run runs the command logic.
func (cmd *SetOptionsCmd) Run(ctx context.Context, context string) error {
	devsyConfig, err := config.LoadConfig("", cmd.Provider)
	if err != nil {
		return err
	}

	// check for context
	if context == "" {
		context = devsyConfig.DefaultContext
	} else if devsyConfig.Contexts[context] == nil {
		return fmt.Errorf("context '%s' doesn't exist", context)
	}

	// check if there are setOptions options set
	if len(cmd.Options) > 0 {
		err = setOptions(devsyConfig, context, cmd.Options)
		if err != nil {
			return err
		}
	}

	err = config.SaveConfig(devsyConfig)
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	return nil
}
