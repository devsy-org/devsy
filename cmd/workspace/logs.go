package workspace

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	clientpkg "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/ssh"
	"github.com/devsy-org/devsy/pkg/tunnel"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// LogsCmd holds the configuration.
type LogsCmd struct {
	*flags.GlobalFlags
}

// NewLogsCmd creates a new destroy command.
func NewLogsCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &LogsCmd{
		GlobalFlags: flags,
	}
	startCmd := &cobra.Command{
		Use:   "logs [flags] [workspace-path|workspace-name]",
		Short: "Prints the workspace logs on the machine",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
		ValidArgsFunction: func(
			rootCmd *cobra.Command, args []string, toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return completion.GetWorkspaceSuggestions(
				rootCmd,
				cmd.Context,
				cmd.Provider,
				args,
				toComplete,
				cmd.Owner,
			)
		},
	}

	return startCmd
}

// Run runs the command logic.
func (cmd *LogsCmd) Run(ctx context.Context, args []string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	baseClient, err := workspace.Get(ctx, workspace.GetOptions{
		DevsyConfig: devsyConfig,
		Args:        args,
		Owner:       cmd.Owner,
	})
	if err != nil {
		return fmt.Errorf("get workspace for logs: %w", err)
	}

	client, ok := baseClient.(clientpkg.WorkspaceClient)
	if !ok {
		return fmt.Errorf("this command is not supported for proxy providers")
	}

	sshServerCmd := fmt.Sprintf("'%s' internal helper ssh-server --stdio", client.AgentPath())
	if log.DebugEnabled() {
		sshServerCmd += " --debug"
	}

	timeout := config.ParseTimeOption(devsyConfig, config.ContextOptionAgentInjectTimeout)

	pb, err := tunnel.NewPipeBridge()
	if err != nil {
		return err
	}
	defer pb.Close()

	return pb.RunPair(ctx,
		func(ctx context.Context, stdin, stdout *os.File) error {
			stderr := log.Writer(log.LevelDebug)
			defer func() { _ = stderr.Close() }()

			return agent.InjectAgent(&agent.InjectOptions{
				Ctx: ctx,
				Exec: func(ctx context.Context, command string, stdinR io.Reader, stdoutW io.Writer, stderrW io.Writer) error {
					return client.Command(ctx, clientpkg.CommandOptions{
						Command: command,
						Stdin:   stdinR,
						Stdout:  stdoutW,
						Stderr:  stderrW,
					})
				},
				IsLocal:         client.AgentLocal(),
				RemoteAgentPath: client.AgentPath(),
				DownloadURL:     client.AgentURL(),
				Command:         sshServerCmd,
				Stdin:           stdin,
				Stdout:          stdout,
				Stderr:          stderr,
				Timeout:         timeout,
			})
		},
		func(ctx context.Context, stdout, stdin *os.File) error {
			sshClient, err := ssh.StdioClientWithUser(stdout, stdin, "", false)
			if err != nil {
				return err
			}
			defer func() { _ = sshClient.Close() }()

			session, err := sshClient.NewSession()
			if err != nil {
				return err
			}
			defer func() { _ = session.Close() }()

			agentCommand := fmt.Sprintf(
				"%s%q internal agent workspace logs --context %q --id %q",
				agent.ContainerAgentEnvPrefix,
				client.AgentPath(),
				client.Context(),
				client.Workspace(),
			)
			if log.DebugEnabled() {
				agentCommand += " --debug"
			}

			session.Stdout = os.Stdout
			session.Stderr = os.Stderr
			return session.Run(agentCommand)
		},
	)
}
