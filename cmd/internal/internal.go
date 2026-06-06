package cmdinternal

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/spf13/cobra"
)

// NewInternalCmd is the hidden parent for plumbing commands invoked by other
// processes (the daemon, the desktop app, container init scripts), not part of
// the user-facing CLI contract.
func NewInternalCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "internal",
		Short:  "internal commands",
		Hidden: true,
	}
	cmd.AddCommand(NewAgentCmd(globalFlags))
	cmd.AddCommand(NewDaemonLocalCmd(globalFlags))
	cmd.AddCommand(NewLogsDaemonCmd(globalFlags))
	cmd.AddCommand(NewRunUserCommandsCmd(globalFlags))
	cmd.AddCommand(NewRunUserCommandsCmdAlias(globalFlags))

	// Utility plumbing commands. The agentPreRunE hook is attached per-command
	// (not on the `internal` parent) so the daemon-local/logs-daemon/
	// run-user-commands children keep inheriting the root command's
	// PersistentPreRunE instead.
	preRun := agentPreRunE(globalFlags)
	withPreRun := func(c *cobra.Command) *cobra.Command {
		c.PersistentPreRunE = preRun
		return c
	}
	cmd.AddCommand(withPreRun(NewHTTPCmd(globalFlags)))
	cmd.AddCommand(withPreRun(NewJSONCmd(globalFlags)))
	cmd.AddCommand(withPreRun(NewStringsCmd(globalFlags)))
	cmd.AddCommand(withPreRun(NewSSHServerCmd(globalFlags)))
	cmd.AddCommand(withPreRun(NewGetWorkspaceNameCmd(globalFlags)))
	cmd.AddCommand(withPreRun(NewGetWorkspaceUIDCmd(globalFlags)))
	cmd.AddCommand(withPreRun(NewGetWorkspaceConfigCommand(globalFlags)))
	cmd.AddCommand(withPreRun(NewGetProviderNameCmd(globalFlags)))
	cmd.AddCommand(withPreRun(NewCheckProviderUpdateCmd(globalFlags)))
	cmd.AddCommand(withPreRun(NewSSHClientCmd()))
	cmd.AddCommand(withPreRun(NewShellCmd()))
	cmd.AddCommand(withPreRun(NewSSHGitCloneCmd()))
	cmd.AddCommand(withPreRun(NewFleetServerCmd(globalFlags)))
	cmd.AddCommand(withPreRun(NewDockerCredentialsHelperCmd(globalFlags)))
	cmd.AddCommand(withPreRun(NewGetImageCmd(globalFlags)))
	cmd.AddCommand(withPreRun(NewGetImagePlatformsCmd(globalFlags)))
	cmd.AddCommand(withPreRun(NewBrowserTunnelCmd(globalFlags)))

	return cmd
}
