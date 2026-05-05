package up

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/sshtunnel"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

func (cmd *UpCmd) devsyUp(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
) (*config2.Result, error) {
	if !cmd.Platform.Enabled {
		err := client.Lock(ctx)
		if err != nil {
			return nil, fmt.Errorf("lock workspace: %w", err)
		}
		defer client.Unlock()
	}

	result, err := cmd.dispatchClient(ctx, devsyConfig, client)
	if err != nil {
		return nil, err
	}

	err = provider2.SaveWorkspaceResult(client.WorkspaceConfig(), result)
	if err != nil {
		return nil, fmt.Errorf("save workspace result: %w", err)
	}

	return result, nil
}

func (cmd *UpCmd) dispatchClient(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
) (*config2.Result, error) {
	switch client := client.(type) {
	case client2.WorkspaceClient:
		return cmd.devsyUpMachine(ctx, devsyConfig, client)
	case client2.ProxyClient:
		return cmd.devsyUpProxy(ctx, client)
	case client2.DaemonClient:
		return cmd.devsyUpDaemon(ctx, client)
	default:
		return nil, fmt.Errorf("unsupported client type: %T", client)
	}
}

func (cmd *UpCmd) devsyUpProxy(
	ctx context.Context,
	client client2.ProxyClient,
) (*config2.Result, error) {
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer func() { _ = stdoutWriter.Close() }()
	defer func() { _ = stdinWriter.Close() }()

	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		defer log.Debug("done executing up command")
		defer cancel()
		errChan <- cmd.runProxyUpCommand(ctx, client, stdinReader, stdoutWriter)
	}()

	result, err := tunnelserver.RunUpServer(
		cancelCtx,
		stdoutReader,
		stdinWriter,
		true,
		true,
		client.WorkspaceConfig(),
	)
	if err != nil {
		return nil, fmt.Errorf("run tunnel machine: %w", err)
	}

	return result, <-errChan
}

func (cmd *UpCmd) runProxyUpCommand(
	ctx context.Context,
	client client2.ProxyClient,
	stdin *os.File,
	stdout *os.File,
) error {
	baseOptions := cmd.buildWorkspaceOptions(client.WorkspaceConfig())

	err := client.Up(ctx, client2.UpOptions{
		CLIOptions: baseOptions,
		Debug:      cmd.Debug,
		Stdin:      stdin,
		Stdout:     stdout,
	})
	if err != nil {
		return fmt.Errorf("executing up proxy command: %w", err)
	}

	return nil
}

func (cmd *UpCmd) devsyUpDaemon(
	ctx context.Context,
	client client2.DaemonClient,
) (*config2.Result, error) {
	baseOptions := cmd.buildWorkspaceOptions(client.WorkspaceConfig())

	return client.Up(ctx, client2.UpOptions{
		CLIOptions: baseOptions,
		Debug:      cmd.Debug,
	})
}

func (cmd *UpCmd) buildWorkspaceOptions(workspace *provider2.Workspace) provider2.CLIOptions {
	baseOptions := cmd.CLIOptions
	baseOptions.ID = workspace.ID
	baseOptions.DevContainerPath = workspace.DevContainerPath
	baseOptions.DevContainerImage = workspace.DevContainerImage
	baseOptions.IDE = workspace.IDE.Name
	baseOptions.IDEOptions = nil
	baseOptions.Source = workspace.Source.String()
	for optionName, optionValue := range workspace.IDE.Options {
		baseOptions.IDEOptions = append(
			baseOptions.IDEOptions,
			optionName+"="+optionValue.Value,
		)
	}

	return baseOptions
}

func (cmd *UpCmd) devsyUpMachine(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.WorkspaceClient,
) (*config2.Result, error) {
	err := clientimplementation.StartWait(ctx, client, true)
	if err != nil {
		return nil, fmt.Errorf("wait for machine: %w", err)
	}

	log.Info("creating devcontainer")
	defer log.Debug("done creating devcontainer")

	if cmd.Platform.Enabled {
		return clientimplementation.BuildAgentClient(
			ctx,
			clientimplementation.BuildAgentClientOptions{
				WorkspaceClient: client,
				CLIOptions:      cmd.CLIOptions,
				AgentCommand:    "up",
				TunnelOptions: []tunnelserver.Option{
					tunnelserver.WithPlatformOptions(&cmd.Platform),
				},
			},
		)
	}

	return cmd.devsyUpMachineSSH(ctx, devsyConfig, client)
}

func (cmd *UpCmd) devsyUpMachineSSH(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.WorkspaceClient,
) (*config2.Result, error) {
	workspaceInfo, wInfo, err := client.AgentInfo(cmd.CLIOptions)
	if err != nil {
		return nil, fmt.Errorf("get agent info: %w", err)
	}

	sshTunnelCmd := fmt.Sprintf("'%s' helper ssh-server --stdio", client.AgentPath())
	if log.DebugEnabled() {
		sshTunnelCmd += " --debug" //nolint:goconst
	}

	agentCommand := fmt.Sprintf(
		"'%s' agent workspace up --workspace-info '%s'",
		client.AgentPath(),
		workspaceInfo,
	)
	if log.DebugEnabled() {
		agentCommand += " --debug"
	}

	return sshtunnel.ExecuteCommand(ctx, sshtunnel.ExecuteCommandOptions{
		Client: client,
		AddPrivateKeys: devsyConfig.ContextOption(
			config.ContextOptionSSHAddPrivateKeys,
		) == config.BoolTrue,
		AgentInject: newAgentInjectFunc(client, wInfo),
		SSHCommand:  sshTunnelCmd,
		Command:     agentCommand,
		TunnelServerFunc: func(ctx context.Context, stdin io.WriteCloser, stdout io.Reader) (*config2.Result, error) {
			return tunnelserver.RunUpServer(
				ctx,
				stdout,
				stdin,
				client.AgentInjectGitCredentials(cmd.CLIOptions),
				client.AgentInjectDockerCredentials(cmd.CLIOptions),
				client.WorkspaceConfig(),
			)
		},
	})
}

func newAgentInjectFunc(
	client client2.WorkspaceClient,
	wInfo *provider2.AgentWorkspaceInfo,
) sshtunnel.AgentInjectFunc {
	return func(
		cancelCtx context.Context, sshCmd string, sshTunnelStdinReader, sshTunnelStdoutWriter *os.File,
		writer io.WriteCloser,
	) error {
		return agent.InjectAgent(&agent.InjectOptions{
			Ctx: cancelCtx,
			Exec: func(ctx context.Context, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
				return client.Command(ctx, client2.CommandOptions{
					Command: command,
					Stdin:   stdin,
					Stdout:  stdout,
					Stderr:  stderr,
				})
			},
			IsLocal:         client.AgentLocal(),
			RemoteAgentPath: client.AgentPath(),
			DownloadURL:     client.AgentURL(),
			Command:         sshCmd,
			Stdin:           sshTunnelStdinReader,
			Stdout:          sshTunnelStdoutWriter,
			Stderr:          writer,
			Timeout:         wInfo.InjectTimeout,
		})
	}
}
