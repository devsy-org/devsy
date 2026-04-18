package context

import (
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/spf13/cobra"
)

// DeleteCmd holds the delete cmd flags.
type DeleteCmd struct {
	*flags.GlobalFlags
}

// NewDeleteCmd deletes a new command.
func NewDeleteCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &DeleteCmd{
		GlobalFlags: flags,
	}
	deleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete a Devsy context",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			if len(args) > 1 {
				return fmt.Errorf("please specify the context to delete")
			}

			devsyContext := ""
			if len(args) == 1 {
				devsyContext = args[0]
			}

			return cmd.Run(cobraCmd.Context(), devsyContext)
		},
	}

	return deleteCmd
}

// Run runs the command logic.
func (cmd *DeleteCmd) Run(ctx context.Context, context string) error {
	devsyConfig, err := config.LoadConfig(context, cmd.Provider)
	if err != nil {
		return err
	}

	// check for context
	if context == "" {
		context = devsyConfig.DefaultContext
	} else if devsyConfig.Contexts[context] == nil {
		return fmt.Errorf("context '%s' doesn't exist", context)
	}

	// check for default context
	if context == "default" {
		return fmt.Errorf("cannot delete 'default' context")
	}

	delete(devsyConfig.Contexts, context)
	if devsyConfig.DefaultContext == context {
		devsyConfig.DefaultContext = "default"
	}
	if devsyConfig.OriginalContext == context {
		devsyConfig.OriginalContext = "default"
	}

	err = config.SaveConfig(devsyConfig)
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	return nil
}
