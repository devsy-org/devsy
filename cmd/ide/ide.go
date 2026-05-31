package ide

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

// NewIDECmd returns a new command.
func NewIDECmd(flags *flags.GlobalFlags) *cobra.Command {
	ideCmd := &cobra.Command{
		Use:   "ide",
		Short: "Devsy IDE commands",
	}

	ideCmd.AddCommand(NewUseCmd(flags))
	ideCmd.AddCommand(NewSetCmd(flags))
	ideCmd.AddCommand(NewOptionsCmd(flags))
	ideCmd.AddCommand(NewListCmd(flags))
	return ideCmd
}
