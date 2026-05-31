package cmdinternal

import (
	"github.com/devsy-org/devsy/cmd/agent"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/helper"
	"github.com/spf13/cobra"
)

// NewInternalCmd is the hidden parent for plumbing commands invoked by other
// processes (the daemon, the desktop app, container init scripts).
// Subcommands here are not part of the user-facing CLI contract.
func NewInternalCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "internal",
		Short:  "Internal plumbing commands (not for direct use)",
		Hidden: true,
	}
	cmd.AddCommand(agent.NewAgentCmd(globalFlags))
	cmd.AddCommand(helper.NewHelperCmd(globalFlags))
	cmd.AddCommand(NewDaemonLocalCmd(globalFlags))
	cmd.AddCommand(NewLogsDaemonCmd(globalFlags))
	cmd.AddCommand(NewRunUserCommandsCmd(globalFlags))
	cmd.AddCommand(NewRunUserCommandsCmdAlias(globalFlags))
	return cmd
}
