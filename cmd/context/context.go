package context

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

// NewContextCmd returns a new command.
func NewContextCmd(flags *flags.GlobalFlags) *cobra.Command {
	contextCmd := &cobra.Command{
		Use:   "context",
		Short: "Manage contexts",
	}

	contextCmd.AddCommand(NewCreateCmd(flags))
	contextCmd.AddCommand(NewDeleteCmd(flags))
	contextCmd.AddCommand(NewUseCmd(flags))
	contextCmd.AddCommand(NewOptionsCmd(flags))
	contextCmd.AddCommand(NewSetOptionsCmd(flags))
	contextCmd.AddCommand(NewListCmd(flags))
	return contextCmd
}
