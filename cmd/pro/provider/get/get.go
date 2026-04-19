package get

import (
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/spf13/cobra"
)

// NewCmd creates a new cobra command.
func NewCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	c := &cobra.Command{
		Use:    "get",
		Short:  "Devsy Pro Provider get commands",
		Args:   cobra.NoArgs,
		Hidden: true,
	}

	c.AddCommand(NewWorkspaceCmd(globalFlags))
	c.AddCommand(NewSelfCmd(globalFlags))
	c.AddCommand(NewVersionCmd(globalFlags))

	return c
}
