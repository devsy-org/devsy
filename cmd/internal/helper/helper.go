package helper

import (
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/internal/agent"
	"github.com/devsy-org/devsy/cmd/internal/helper/http"
	"github.com/devsy-org/devsy/cmd/internal/helper/json"
	"github.com/devsy-org/devsy/cmd/internal/helper/ssh"
	"github.com/devsy-org/devsy/cmd/internal/helper/strings"
	"github.com/devsy-org/devsy/cmd/internal/helper/workspaceinfo"
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

	helperCmd.AddCommand(http.NewHTTPCmd(globalFlags))
	helperCmd.AddCommand(json.NewJSONCmd(globalFlags))
	helperCmd.AddCommand(strings.NewStringsCmd(globalFlags))
	helperCmd.AddCommand(ssh.NewSSHServerCmd(globalFlags))
	helperCmd.AddCommand(workspaceinfo.NewGetWorkspaceNameCmd(globalFlags))
	helperCmd.AddCommand(workspaceinfo.NewGetWorkspaceUIDCmd(globalFlags))
	helperCmd.AddCommand(workspaceinfo.NewGetWorkspaceConfigCommand(globalFlags))
	helperCmd.AddCommand(NewGetProviderNameCmd(globalFlags))
	helperCmd.AddCommand(NewCheckProviderUpdateCmd(globalFlags))
	helperCmd.AddCommand(ssh.NewSSHClientCmd())
	helperCmd.AddCommand(NewShellCmd())
	helperCmd.AddCommand(ssh.NewSSHGitCloneCmd())
	helperCmd.AddCommand(NewFleetServerCmd(globalFlags))
	helperCmd.AddCommand(NewDockerCredentialsHelperCmd(globalFlags))
	helperCmd.AddCommand(NewGetImageCmd(globalFlags))
	helperCmd.AddCommand(NewGetImagePlatformsCmd(globalFlags))
	helperCmd.AddCommand(NewBrowserTunnelCmd(globalFlags))
	return helperCmd
}
