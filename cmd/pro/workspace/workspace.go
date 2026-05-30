package workspace

import (
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/spf13/cobra"
)

// NewWorkspaceCmd builds the 'devsy pro workspace' parent command for managing
// Pro workspaces.
func NewWorkspaceCmd(globalFlags *proflags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage Devsy Pro workspaces",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(NewListCmd(globalFlags))
	cmd.AddCommand(NewCreateCmd(globalFlags))
	cmd.AddCommand(NewUpdateCmd(globalFlags))
	cmd.AddCommand(NewImportCmd(globalFlags))
	cmd.AddCommand(NewRebuildCmd(globalFlags))
	cmd.AddCommand(NewSleepCmd(globalFlags))
	cmd.AddCommand(NewWakeupCmd(globalFlags))
	cmd.AddCommand(NewWatchCmd(globalFlags))
	return cmd
}
