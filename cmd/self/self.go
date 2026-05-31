package self

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

// NewSelfCmd builds the 'devsy self' parent command for managing the devsy CLI itself.
func NewSelfCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "self",
		Short: "Manage the devsy CLI itself",
	}
	cmd.AddCommand(NewUpdateCmd())
	return cmd
}
