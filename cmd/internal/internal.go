package cmdinternal

import (
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/internal/agentcontainer"
	"github.com/devsy-org/devsy/cmd/internal/agentworkspace"
	"github.com/devsy-org/devsy/cmd/internal/helperhttp"
	"github.com/devsy-org/devsy/cmd/internal/helperimage"
	"github.com/devsy-org/devsy/cmd/internal/helperjson"
	"github.com/devsy-org/devsy/cmd/internal/helperprovider"
	"github.com/devsy-org/devsy/cmd/internal/helperssh"
	"github.com/devsy-org/devsy/cmd/internal/helperstrings"
	"github.com/devsy-org/devsy/cmd/internal/helperworkspaceinfo"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envfile"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

var AgentExecutedAnnotation = "devsy.sh/agent-executed"

// NewInternalCmd is the hidden parent for plumbing commands invoked by other
// processes (the daemon, the desktop app, container init scripts).
// Subcommands here are not part of the user-facing CLI contract.
func NewInternalCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "internal",
		Short:  "Internal plumbing commands (not for direct use)",
		Hidden: true,
	}
	cmd.AddCommand(NewAgentCmd(globalFlags))
	cmd.AddCommand(NewHelperCmd(globalFlags))
	cmd.AddCommand(NewDaemonLocalCmd(globalFlags))
	cmd.AddCommand(NewLogsDaemonCmd(globalFlags))
	cmd.AddCommand(NewRunUserCommandsCmd(globalFlags))
	cmd.AddCommand(NewRunUserCommandsCmdAlias(globalFlags))
	return cmd
}

// NewAgentCmd returns a new root command.
func NewAgentCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	agentCmd := &cobra.Command{
		Use:   "agent",
		Short: "Devsy Agent",
		PersistentPreRunE: func(cobraCmd *cobra.Command, args []string) error {
			return AgentPersistentPreRunE(cobraCmd, args, globalFlags)
		},
		Hidden: true,
	}

	agentCmd.AddCommand(agentworkspace.NewWorkspaceCmd(globalFlags))
	agentCmd.AddCommand(agentcontainer.NewContainerCmd(globalFlags))
	agentCmd.AddCommand(NewDaemonCmd(globalFlags))
	agentCmd.AddCommand(NewContainerTunnelCmd(globalFlags))
	agentCmd.AddCommand(NewGitCredentialsCmd(globalFlags))
	agentCmd.AddCommand(NewGitSSHSignatureCmd(globalFlags))
	agentCmd.AddCommand(NewGitSSHSignatureHelperCmd(globalFlags))
	agentCmd.AddCommand(NewDockerCredentialsCmd(globalFlags))
	return agentCmd
}

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

func AgentPersistentPreRunE(
	cobraCmd *cobra.Command,
	args []string,
	globalFlags *flags.GlobalFlags,
) error {
	// get top level parent
	parent := cobraCmd
	for parent.Parent() != nil {
		parent = parent.Parent()
	}
	if parent.Annotations == nil {
		parent.Annotations = map[string]string{}
	}
	parent.Annotations[AgentExecutedAnnotation] = "true"

	// Initialise the zap logger for the agent subprocess.
	// stdout is the binary protocol channel, so all log output goes to stderr.
	log.Init(log.Config{
		Quiet:  globalFlags.Quiet,
		Debug:  globalFlags.Debug,
		Format: "json", // Agent must use JSON: single-line output is captured by TunnelLogStreamer.lastLines
	})

	if globalFlags.DevsyHome != "" {
		_ = os.Setenv(config.EnvHome, globalFlags.DevsyHome)
	}

	// apply environment
	envfile.Apply()
	return nil
}
