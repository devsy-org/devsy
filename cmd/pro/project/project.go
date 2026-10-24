package project

import (
	platformflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/spf13/cobra"
)

// NewProjectCmd builds the 'devsy platform project' parent command for managing
// Platform projects.
func NewProjectCmd(globalFlags *platformflags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage Devsy Platform projects",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(NewListCmd(globalFlags))
	return cmd
}
