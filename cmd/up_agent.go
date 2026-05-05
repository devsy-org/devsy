package cmd

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

func (cmd *UpCmd) devsyUp( //nolint:cyclop
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
) (*config2.Result, error) {
	var err error

	// only lock if we are not in platform mode
	if !cmd.Platform.Enabled {
		err := client.Lock(ctx)
		if err != nil {
			return nil, fmt.Errorf("lock workspace: %w", err)
		}
		defer client.Unlock()
	}

	// get result
	var result *config2.Result

	switch client := client.(type) {
	case client2.WorkspaceClient:
		result, err = cmd.devsyUpMachine(ctx, devsyConfig, client)
		if err != nil {
			return nil, err
		}
	case client2.ProxyClient:
		result, err = cmd.devsyUpProxy(ctx, client)
		if err != nil {
			return nil, err
		}
	case client2.DaemonClient:
		result, err = cmd.devsyUpDaemon(ctx, client)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported client type: %T", client)
	}

	// save result to file
	err = provider2.SaveWorkspaceResult(client.WorkspaceConfig(), result)
	if err != nil {
		return nil, fmt.Errorf("save workspace result: %w", err)
	}

	return result, nil
}

func (cmd *UpCmd) devsyUpProxy( //nolint:funlen
	ctx context.Context,
	client client2.ProxyClient,
) (*config2.Result, error) {
	// create pipes
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

	// start machine on stdio
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// create up command
	errChan := make(chan error, 1)
	go func() {
		defer log.Debug("done executing up command")
		defer cancel()

		// build devsy up options
		workspace := client.WorkspaceConfig()
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

		// run devsy up elsewhere
		err = client.Up(ctx, client2.UpOptions{
			CLIOptions: baseOptions,
			Debug:      cmd.Debug,

			Stdin:  stdinReader,
			Stdout: stdoutWriter,
		})
		if err != nil {
			errChan <- fmt.Errorf("executing up proxy command: %w", err)
		} else {
			errChan <- nil
		}
	}()

	// create container etc.
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

	// wait until command finished
	return result, <-errChan
}

func (cmd *UpCmd) devsyUpDaemon(
	ctx context.Context,
	client client2.DaemonClient,
) (*config2.Result, error) {
	// build devsy up options
	workspace := client.WorkspaceConfig()
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

	// run devsy up elsewhere
	return client.Up(ctx, client2.UpOptions{
		CLIOptions: baseOptions,
		Debug:      cmd.Debug,
	})
}

func (cmd *UpCmd) devsyUpMachine( //nolint:funlen
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.WorkspaceClient,
) (*config2.Result, error) {
	err := clientimplementation.StartWait(ctx, client, true)
	if err != nil {
		return nil, fmt.Errorf("wait for machine: %w", err)
	}

	// compress info
	workspaceInfo, wInfo, err := client.AgentInfo(cmd.CLIOptions)
	if err != nil {
		return nil, fmt.Errorf("get agent info: %w", err)
	}

	// create container etc.
	log.Info("creating devcontainer")
	defer log.Debug("done creating devcontainer")

	// if we run on a platform, we need to pass the platform options
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

	// ssh tunnel command
	sshTunnelCmd := fmt.Sprintf("'%s' helper ssh-server --stdio", client.AgentPath())
	if log.DebugEnabled() {
		sshTunnelCmd += " --debug" //nolint:goconst
	}

	// create agent command
	agentCommand := fmt.Sprintf(
		"'%s' agent workspace up --workspace-info '%s'",
		client.AgentPath(),
		workspaceInfo,
	)

	if log.DebugEnabled() {
		agentCommand += " --debug"
	}

	agentInjectFunc := func(
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

	return sshtunnel.ExecuteCommand(ctx, sshtunnel.ExecuteCommandOptions{
		Client: client,
		AddPrivateKeys: devsyConfig.ContextOption(
			config.ContextOptionSSHAddPrivateKeys,
		) == config.BoolTrue,
		AgentInject: agentInjectFunc,
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
