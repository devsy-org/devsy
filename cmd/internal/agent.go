package cmdinternal

import (
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/internal/agentcontainer"
	"github.com/devsy-org/devsy/cmd/internal/agentworkspace"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envfile"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

var AgentExecutedAnnotation = "devsy.sh/agent-executed"

// NewAgentCmd is the hidden parent for commands that run inside a workspace or
// container, invoked by the daemon over the agent tunnel.
func NewAgentCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	agentCmd := &cobra.Command{
		Use:               "agent",
		Short:             "Devsy Agent",
		PersistentPreRunE: agentPreRunE(globalFlags),
		Hidden:            true,
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

// agentPreRunE builds the PersistentPreRunE shared by the agent command and the
// utility plumbing commands. Logging is forced to JSON because the agent
// subprocess uses stdout as a binary protocol channel, so log output must stay
// on stderr as single-line JSON captured by TunnelLogStreamer.lastLines.
func agentPreRunE(globalFlags *flags.GlobalFlags) func(*cobra.Command, []string) error {
	return func(cobraCmd *cobra.Command, _ []string) error {
		root := cobraCmd
		for root.Parent() != nil {
			root = root.Parent()
		}
		if root.Annotations == nil {
			root.Annotations = map[string]string{}
		}
		root.Annotations[AgentExecutedAnnotation] = "true"

		log.Init(log.Config{
			Quiet:  globalFlags.Quiet,
			Debug:  globalFlags.Debug,
			Format: "json",
		})

		if globalFlags.DevsyHome != "" {
			_ = os.Setenv(config.EnvHome, globalFlags.DevsyHome)
		}

		envfile.Apply()
		return nil
	}
}
