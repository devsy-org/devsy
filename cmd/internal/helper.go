package cmdinternal

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/internal/helperhttp"
	"github.com/devsy-org/devsy/cmd/internal/helperimage"
	"github.com/devsy-org/devsy/cmd/internal/helperjson"
	"github.com/devsy-org/devsy/cmd/internal/helperprovider"
	"github.com/devsy-org/devsy/cmd/internal/helperssh"
	"github.com/devsy-org/devsy/cmd/internal/helperstrings"
	"github.com/devsy-org/devsy/cmd/internal/helperworkspaceinfo"
	"github.com/spf13/cobra"
)

// NewHelperCmd returns a new command.
func NewHelperCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	helperCmd := &cobra.Command{
		Use:   "helper",
		Short: "Devsy Utility Commands",
		PersistentPreRunE: func(cobraCmd *cobra.Command, args []string) error {
			return AgentPersistentPreRunE(cobraCmd, args, globalFlags)
		},
		Hidden: true,
	}

	helperCmd.AddCommand(helperhttp.NewHTTPCmd(globalFlags))
	helperCmd.AddCommand(helperjson.NewJSONCmd(globalFlags))
	helperCmd.AddCommand(helperstrings.NewStringsCmd(globalFlags))
	helperCmd.AddCommand(helperssh.NewSSHServerCmd(globalFlags))
	helperCmd.AddCommand(helperworkspaceinfo.NewGetWorkspaceNameCmd(globalFlags))
	helperCmd.AddCommand(helperworkspaceinfo.NewGetWorkspaceUIDCmd(globalFlags))
	helperCmd.AddCommand(helperworkspaceinfo.NewGetWorkspaceConfigCommand(globalFlags))
	helperCmd.AddCommand(helperprovider.NewGetProviderNameCmd(globalFlags))
	helperCmd.AddCommand(helperprovider.NewCheckProviderUpdateCmd(globalFlags))
	helperCmd.AddCommand(helperssh.NewSSHClientCmd())
	helperCmd.AddCommand(NewShellCmd())
	helperCmd.AddCommand(helperssh.NewSSHGitCloneCmd())
	helperCmd.AddCommand(NewFleetServerCmd(globalFlags))
	helperCmd.AddCommand(NewDockerCredentialsHelperCmd(globalFlags))
	helperCmd.AddCommand(helperimage.NewGetImageCmd(globalFlags))
	helperCmd.AddCommand(helperimage.NewGetImagePlatformsCmd(globalFlags))
	helperCmd.AddCommand(NewBrowserTunnelCmd(globalFlags))
	return helperCmd
}
