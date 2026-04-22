package agent

import (
	"os"

	"github.com/devsy-org/devsy/cmd/agent/container"
	"github.com/devsy-org/devsy/cmd/agent/workspace"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/envfile"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

var AgentExecutedAnnotation = "devsy.sh/agent-executed"

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

	agentCmd.AddCommand(workspace.NewWorkspaceCmd(globalFlags))
	agentCmd.AddCommand(container.NewContainerCmd(globalFlags))
	agentCmd.AddCommand(NewDaemonCmd(globalFlags))
	agentCmd.AddCommand(NewContainerTunnelCmd(globalFlags))
	agentCmd.AddCommand(NewGitCredentialsCmd(globalFlags))
	agentCmd.AddCommand(NewGitSSHSignatureCmd(globalFlags))
	agentCmd.AddCommand(NewGitSSHSignatureHelperCmd(globalFlags))
	agentCmd.AddCommand(NewDockerCredentialsCmd(globalFlags))
	return agentCmd
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
	// "raw" outputs message text only (no timestamp/level/caller/stacktrace),
	// matching the old MakeRaw() behavior. This is required because agent stderr
	// is captured by TunnelLogStreamer.ErrorOutput and embedded in the parent's
	// error chain; JSON or console format would cause double-escaping or
	// multi-line stacktraces that get truncated to the last line.
	log.Init(log.Config{
		Quiet:  globalFlags.Quiet,
		Debug:  globalFlags.Debug,
		Format: "raw",
	})

	if globalFlags.DevsyHome != "" {
		_ = os.Setenv(config.EnvHome, globalFlags.DevsyHome)
	}

	// apply environment
	envfile.Apply()
	return nil
}
