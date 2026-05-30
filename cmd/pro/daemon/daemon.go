package daemon

import (
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/spf13/cobra"
)

// NewCmd creates a new cobra command.
func NewCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	c := &cobra.Command{
		Use:    "daemon",
		Short:  "Devsy Pro Provider daemon commands",
		Args:   cobra.NoArgs,
		Hidden: true,
	}

	c.AddCommand(NewStartCmd(globalFlags))
	c.AddCommand(NewStatusCmd(globalFlags))
	c.AddCommand(NewNetcheckCmd(globalFlags))

	return c
}
