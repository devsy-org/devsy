// Package dependency provides passive inspection of installed provider dependencies.
package dependency

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

func NewDependencyCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	command := &cobra.Command{
		Use: "dependency", Short: "Inspect installed provider dependencies",
	}
	flags.MarkPassive(command)
	flags.MarkDependencyInspection(command)
	command.AddCommand(newInspectCmd(globalFlags))
	return command
}
