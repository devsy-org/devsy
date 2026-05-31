package workspace

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/workspace/up"
	"github.com/spf13/cobra"
)

// NewWorkspaceCmd builds the 'devsy workspace' parent command.
func NewWorkspaceCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage devcontainer workspaces",
	}
	cmd.AddCommand(up.NewUpCmd(globalFlags))
	cmd.AddCommand(NewStopCmd(globalFlags))
	cmd.AddCommand(NewDeleteCmd(globalFlags))
	cmd.AddCommand(NewSSHCmd(globalFlags))
	cmd.AddCommand(NewExecCmd(globalFlags))
	cmd.AddCommand(NewListCmd(globalFlags))
	cmd.AddCommand(NewStatusCmd(globalFlags))
	cmd.AddCommand(NewLogsCmd(globalFlags))
	cmd.AddCommand(NewBuildCmd(globalFlags))
	cmd.AddCommand(NewRenameCmd(globalFlags))
	cmd.AddCommand(NewSetIDECmd(globalFlags))
	cmd.AddCommand(NewExportCmd(globalFlags))
	cmd.AddCommand(NewImportCmd(globalFlags))
	cmd.AddCommand(NewPingCmd(globalFlags))
	cmd.AddCommand(NewTroubleshootCmd(globalFlags))
	return cmd
}
