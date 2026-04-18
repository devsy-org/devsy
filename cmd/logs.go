package cmd

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
	"github.com/devsy-org/devsy/pkg/ssh"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/devsy-org/log"
	"github.com/sirupsen/logrus"
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
				log.Default,
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
		Log:         log.Default,
	})
	if err != nil {
		return err
	}

	client, ok := baseClient.(clientpkg.WorkspaceClient)
	if !ok {
		return fmt.Errorf("this command is not supported for proxy providers")
	}
	log := log.Default

	// create readers
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		return err
	}
	defer func() { _ = stdoutWriter.Close() }()
	defer func() { _ = stdinWriter.Close() }()
	// ssh tunnel command
	sshServerCmd := fmt.Sprintf("'%s' helper ssh-server --stdio", client.AgentPath())
	if log.GetLevel() == logrus.DebugLevel {
		sshServerCmd += " --debug"
	}

	// Get the timeout from the context options
	timeout := config.ParseTimeOption(devsyConfig, config.ContextOptionAgentInjectTimeout)

	// start ssh server in background
	errChan := make(chan error, 1)
	go func() {
		stderr := log.ErrorStreamOnly().Writer(logrus.DebugLevel, false)
		defer func() { _ = stderr.Close() }()

		errChan <- agent.InjectAgent(&agent.InjectOptions{
			Ctx: ctx,
			Exec: func(ctx context.Context, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
				return client.Command(ctx, clientpkg.CommandOptions{
					Command: command,
					Stdin:   stdin,
					Stdout:  stdout,
					Stderr:  stderr,
				})
			},
			IsLocal:         client.AgentLocal(),
			RemoteAgentPath: client.AgentPath(),
			DownloadURL:     client.AgentURL(),
			Command:         sshServerCmd,
			Stdin:           stdinReader,
			Stdout:          stdoutWriter,
			Stderr:          stderr,
			Log:             log.ErrorStreamOnly(),
			Timeout:         timeout,
		})
	}()

	// create agent command
	agentCommand := fmt.Sprintf(
		"'%s' agent workspace logs --context '%s' --id '%s'",
		client.AgentPath(),
		client.Context(),
		client.Workspace(),
	)
	if log.GetLevel() == logrus.DebugLevel {
		agentCommand += " --debug"
	}

	// create new ssh client
	// start ssh client as root / default user
	sshClient, err := ssh.StdioClientWithUser(stdoutReader, stdinWriter, "" /* default */, false)
	if err != nil {
		return err
	}
	defer func() { _ = sshClient.Close() }()

	session, err := sshClient.NewSession()
	if err != nil {
		return err
	}
	defer func() { _ = session.Close() }()

	session.Stdout = os.Stdout
	session.Stderr = os.Stderr
	err = session.Run(agentCommand)
	if err != nil {
		return err
	}

	return nil
}
