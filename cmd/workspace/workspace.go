package workspace

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

// NewWorkspaceCmd builds the `devsy workspace` parent command.
// Subcommands are attached by the caller, since some of the same factories
// are also wired as root shortcuts (devsy up == devsy workspace up).
func NewWorkspaceCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workspace",
		Short: "Manage devcontainer workspaces",
	}
	return cmd
}
