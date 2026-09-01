package workspace

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

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
func NewLogsCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &LogsCmd{
		GlobalFlags: globalFlags,
	}
	startCmd := &cobra.Command{
		Use:   "logs [flags] [workspace-path|workspace-name]",
		Short: "Print workspace logs",
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

	client, err := cmd.getWorkspaceClient(ctx, devsyConfig, args)
	if err != nil {
		return err
	}

	sshServerCmd := fmt.Sprintf("'%s' internal ssh-server --stdio", client.AgentPath())
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
			return injectLogsAgent(ctx, injectLogsAgentParams{
				client:       client,
				sshServerCmd: sshServerCmd,
				timeout:      timeout,
				stdin:        stdin,
				stdout:       stdout,
			})
		},
		func(ctx context.Context, stdout, stdin *os.File) error {
			return runLogsSession(stdout, stdin, client)
		},
	)
}

// getWorkspaceClient resolves args to a WorkspaceClient, rejecting proxy providers
// which don't support log streaming.
func (cmd *LogsCmd) getWorkspaceClient(
	ctx context.Context, devsyConfig *config.Config, args []string,
) (clientpkg.WorkspaceClient, error) {
	baseClient, err := workspace.Get(ctx, workspace.GetOptions{
		DevsyConfig: devsyConfig,
		Args:        args,
		Owner:       cmd.Owner,
	})
	if err != nil {
		return nil, fmt.Errorf("get workspace for logs: %w", err)
	}

	client, ok := baseClient.(clientpkg.WorkspaceClient)
	if !ok {
		return nil, fmt.Errorf("this command is not supported for proxy providers")
	}

	return client, nil
}

// injectLogsAgent injects the devsy agent binary over stdin/stdout and runs the
// remote ssh-server that runLogsSession then connects to.
func injectLogsAgent(ctx context.Context, params injectLogsAgentParams) error {
	streamer := log.NewJSONLogStreamer(log.StreamerOptions{
		FallbackLevel:       log.LevelDebug,
		DetectLevelPrefixes: true,
	})
	defer func() { _ = streamer.Close() }()

	return agent.InjectAgent(ctx, &agent.InjectOptions{
		Exec: func(ctx context.Context, command string, stdinR io.Reader, stdoutW io.Writer, stderrW io.Writer) error {
			return params.client.Command(ctx, clientpkg.CommandOptions{
				Command: command,
				Stdin:   stdinR,
				Stdout:  stdoutW,
				Stderr:  stderrW,
			})
		},
		IsLocal:         params.client.AgentLocal(),
		RemoteAgentPath: params.client.AgentPath(),
		DownloadURL:     params.client.AgentURL(),
		Command:         params.sshServerCmd,
		Stdin:           params.stdin,
		Stdout:          params.stdout,
		Stderr:          streamer,
		Timeout:         params.timeout,
	})
}

type injectLogsAgentParams struct {
	client       clientpkg.WorkspaceClient
	sshServerCmd string
	timeout      time.Duration
	stdin        *os.File
	stdout       *os.File
}

func runLogsSession(stdout, stdin *os.File, client clientpkg.WorkspaceClient) error {
	sshClient, err := ssh.StdioClientWithUser(stdout, stdin, "")
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
		"%q internal agent workspace logs --context %q --id %q",
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
}
