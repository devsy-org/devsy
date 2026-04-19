package add

import (
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/spf13/cobra"
)

// NewAddCmd creates a new command.
func NewAddCmd(globalFlags *proflags.GlobalFlags) *cobra.Command {
	addCmd := &cobra.Command{
		Use:   "add",
		Short: "Adds a given resource to Devsy Pro",
		Args:  cobra.NoArgs,
	}

	addCmd.AddCommand(NewClusterCmd(globalFlags))
	return addCmd
}
