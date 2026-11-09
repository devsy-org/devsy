package up

import (
	"context"
	"fmt"
	"io"
	"os"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/sshtunnel"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/secrets"
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
		// Preserve a structured error envelope the agent may have forwarded
		// over the tunnel before exiting non-zero (e.g. host requirements
		// not met), so the caller can surface the real cause.
		if result.Err() != nil {
			return result, err
		}
		return nil, err
	}

	err = provider2.SaveWorkspaceResult(client.WorkspaceConfig(), result)
	if err != nil {
		return nil, fmt.Errorf("save workspace result: %w", err)
	}

	return result, nil
}

func (cmd *UpCmd) validateInteractiveRoute(client client2.BaseWorkspaceClient) error {
	if !cmd.InteractivePostStart {
		return nil
	}
	if cmd.Platform.Enabled {
		return fmt.Errorf(
			"--interactive-post-start is unsupported with platform-managed workspaces",
		)
	}
	switch client.(type) {
	case client2.WorkspaceClient:
		return nil
	case client2.ProxyClient:
		return fmt.Errorf("--interactive-post-start is unsupported with proxy workspaces")
	case client2.DaemonClient:
		return fmt.Errorf("--interactive-post-start is unsupported with daemon workspaces")
	}
	return nil
}

func (cmd *UpCmd) dispatchClient(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
) (*config2.Result, error) {
	if cmd.InteractivePostStart && cmd.Platform.Enabled {
		return nil, fmt.Errorf(
			"--interactive-post-start is unsupported with platform-managed workspaces",
		)
	}
	switch client := client.(type) {
	case client2.WorkspaceClient:
		return cmd.devsyUpMachine(ctx, devsyConfig, client)
	case client2.ProxyClient:
		if cmd.InteractivePostStart {
			return nil, fmt.Errorf("--interactive-post-start is unsupported with proxy workspaces")
		}
		return cmd.devsyUpProxy(ctx, client)
	case client2.DaemonClient:
		if cmd.InteractivePostStart {
			return nil, fmt.Errorf("--interactive-post-start is unsupported with daemon workspaces")
		}
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

	result, err := tunnelserver.RunUpServer(cancelCtx, tunnelserver.UpSpec{
		Reader:                 stdoutReader,
		Writer:                 stdinWriter,
		AllowGitCredentials:    true,
		AllowDockerCredentials: true,
		Workspace:              client.WorkspaceConfig(),
		Options: append([]tunnelserver.Option{
			tunnelserver.WithGitToken(cmd.GitToken),
			tunnelserver.WithStatusReporter(cmd.reporter()),
		}, cmd.setupHop.Options()...),
	})
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
		Reporter:   cmd.reporter(),
	})
}

func (cmd *UpCmd) buildWorkspaceOptions(workspace *provider2.Workspace) provider2.CLIOptions {
	baseOptions := cmd.CLIOptions
	baseOptions.ID = workspace.ID
	if workspace.DevContainerConfig == nil {
		baseOptions.DevContainerPath = workspace.DevContainerPath
		baseOptions.DevContainerID = workspace.DevContainerID
	}
	baseOptions.DevContainerImage = workspace.DevContainerImage
	baseOptions.DevContainerSource = workspace.DevContainerSource
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

	log.Debug("creating devcontainer")
	defer log.Debug("done creating devcontainer")

	if cmd.Platform.Enabled {
		cliOptions := withoutSecretValues(cmd.CLIOptions)
		return clientimplementation.BuildAgentClient(
			ctx,
			clientimplementation.BuildAgentClientOptions{
				WorkspaceClient: client,
				CLIOptions:      cliOptions,
				AgentCommand:    "up",
				TunnelOptions: []tunnelserver.Option{
					tunnelserver.WithPlatformOptions(&cmd.Platform),
					tunnelserver.WithSecrets(cmd.SecretsEnv, cmd.SecretsMount),
					tunnelserver.WithStatusReporter(cmd.reporter()),
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
	var owner *interactivePostStart
	var lifecycleOption tunnelserver.Option
	if request := cmd.LifecycleTerminal; request != nil {
		gate := terminalStatusGate(cmd.reporter())
		owner = newInteractivePostStart(interactivePostStartOptions{
			stdin: os.Stdin, stdout: os.Stdout, rows: cmd.TerminalRows,
			columns: cmd.TerminalColumns, term: os.Getenv("TERM"),
			status: gate, output: cmd.stdout(),
		})
		defer owner.Close()
		lifecycleOption = tunnelserver.WithLifecycleTerminalOwner(owner, *request)
	}
	workspaceInfo, wInfo, err := client.AgentInfo(withoutSecretValues(cmd.CLIOptions))
	if err != nil {
		return nil, fmt.Errorf("get agent info: %w", err)
	}

	sshTunnelCmd := shellescape.Quote(client.AgentPath()) + " internal ssh-server --stdio"
	if log.DebugEnabled() {
		sshTunnelCmd += " --debug" //nolint:goconst
	}

	agentCommand := workspaceAgentCommand(
		client.AgentPath(),
		workspaceInfo,
		log.DebugEnabled(),
		cmd.LifecycleTerminal != nil,
	)

	options := append([]tunnelserver.Option{
		tunnelserver.WithSecrets(cmd.SecretsEnv, cmd.SecretsMount),
		tunnelserver.WithGitToken(cmd.GitToken),
		tunnelserver.WithStatusReporter(cmd.reporter()),
	}, cmd.setupHop.Options()...)
	if lifecycleOption != nil {
		options = append(options, lifecycleOption)
	}
	return sshtunnel.ExecuteCommand(ctx, sshtunnel.ExecuteCommandOptions{
		Client: client,
		AddPrivateKeys: devsyConfig.ContextOption(
			config.ContextOptionSSHAddPrivateKeys,
		) == config.BoolTrue,
		AgentInject: newAgentInjectFunc(client, wInfo),
		SSHCommand:  sshTunnelCmd,
		Command:     agentCommand,
		Redactor:    workspaceSecretRedactor(cmd),
		TunnelServerFunc: func(ctx context.Context, stdin io.WriteCloser, stdout io.Reader) (*config2.Result, error) {
			return tunnelserver.RunUpServer(ctx, tunnelserver.UpSpec{
				Reader:                 stdout,
				Writer:                 stdin,
				AllowGitCredentials:    client.AgentInjectGitCredentials(cmd.CLIOptions),
				AllowDockerCredentials: client.AgentInjectDockerCredentials(cmd.CLIOptions),
				Workspace:              client.WorkspaceConfig(),
				Options:                options,
			})
		},
	})
}

func workspaceSecretRedactor(cmd *UpCmd) *secrets.Redactor {
	var token []string
	if cmd.GitToken != nil {
		token = append(token, "TOKEN="+cmd.GitToken.Token)
	}
	return secrets.Combine(
		secrets.NewRedactor(cmd.SecretsEnv),
		secrets.NewRedactor(cmd.SecretsMount),
		secrets.NewRedactor(cmd.BuildSecrets),
		secrets.NewRedactor(token),
	)
}

func workspaceAgentCommand(agentPath, workspaceInfo string, debug, lifecycleTerminal bool) string {
	command := fmt.Sprintf(
		"%s internal agent workspace up --workspace-info %s",
		shellescape.Quote(agentPath),
		shellescape.Quote(workspaceInfo),
	)
	if debug {
		command += " --debug"
	}
	if lifecycleTerminal {
		command += " " + shellescape.Quote("--lifecycle-terminal-protocol=1")
	}
	return command
}

func withoutSecretValues(options provider2.CLIOptions) provider2.CLIOptions {
	options.SecretsEnv = nil
	options.SecretsMount = nil
	return options
}

func newAgentInjectFunc(
	client client2.WorkspaceClient,
	wInfo *provider2.AgentWorkspaceInfo,
) sshtunnel.AgentInjectFunc {
	return func(
		cancelCtx context.Context,
		sshCmd string,
		sshTunnelStdinReader io.Reader, sshTunnelStdoutWriter io.Writer,
		writer io.WriteCloser,
	) error {
		return agent.InjectAgent(cancelCtx, &agent.InjectOptions{
			Exec: func(ctx context.Context, command string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
				return client.Command(ctx, client2.CommandOptions{
					Command:   command,
					Stdin:     stdin,
					Stdout:    stdout,
					Stderr:    stderr,
					RawStdout: true,
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
