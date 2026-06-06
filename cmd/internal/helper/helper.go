package helper

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/internal/agent"
	"github.com/devsy-org/devsy/cmd/internal/helper/image"
	"github.com/devsy-org/devsy/cmd/internal/helper/provider"
	"github.com/devsy-org/devsy/cmd/internal/helper/ssh"
	"github.com/devsy-org/devsy/cmd/internal/helper/workspaceinfo"
	"github.com/devsy-org/devsy/cmd/internal/helperhttp"
	"github.com/devsy-org/devsy/cmd/internal/helperjson"
	"github.com/devsy-org/devsy/cmd/internal/helperstrings"
	"github.com/spf13/cobra"
)

// NewHelperCmd returns a new command.
func NewHelperCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	helperCmd := &cobra.Command{
		Use:   "helper",
		Short: "Devsy Utility Commands",
		PersistentPreRunE: func(cobraCmd *cobra.Command, args []string) error {
			return agent.AgentPersistentPreRunE(cobraCmd, args, globalFlags)
		},
		Hidden: true,
	}

	helperCmd.AddCommand(helperhttp.NewHTTPCmd(globalFlags))
	helperCmd.AddCommand(helperjson.NewJSONCmd(globalFlags))
	helperCmd.AddCommand(helperstrings.NewStringsCmd(globalFlags))
	helperCmd.AddCommand(ssh.NewSSHServerCmd(globalFlags))
	helperCmd.AddCommand(workspaceinfo.NewGetWorkspaceNameCmd(globalFlags))
	helperCmd.AddCommand(workspaceinfo.NewGetWorkspaceUIDCmd(globalFlags))
	helperCmd.AddCommand(workspaceinfo.NewGetWorkspaceConfigCommand(globalFlags))
	helperCmd.AddCommand(provider.NewGetProviderNameCmd(globalFlags))
	helperCmd.AddCommand(provider.NewCheckProviderUpdateCmd(globalFlags))
	helperCmd.AddCommand(ssh.NewSSHClientCmd())
	helperCmd.AddCommand(NewShellCmd())
	helperCmd.AddCommand(ssh.NewSSHGitCloneCmd())
	helperCmd.AddCommand(NewFleetServerCmd(globalFlags))
	helperCmd.AddCommand(NewDockerCredentialsHelperCmd(globalFlags))
	helperCmd.AddCommand(image.NewGetImageCmd(globalFlags))
	helperCmd.AddCommand(image.NewGetImagePlatformsCmd(globalFlags))
	helperCmd.AddCommand(NewBrowserTunnelCmd(globalFlags))
	return helperCmd
}
