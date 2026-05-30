package project

import (
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/spf13/cobra"
)

// NewProjectCmd builds the 'devsy pro project' parent command for managing
// Pro projects.
func NewProjectCmd(globalFlags *proflags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage Devsy Pro projects",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(NewListCmd(globalFlags))
	return cmd
}
