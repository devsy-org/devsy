package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/agent"
	"github.com/devsy-org/devsy/pkg/agent/tunnelserver"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/sshtunnel"
	"github.com/devsy-org/devsy/pkg/dotfiles"
	"github.com/devsy-org/devsy/pkg/ide"
	"github.com/devsy-org/devsy/pkg/ide/opener"
	devsylog "github.com/devsy-org/devsy/pkg/log"
	options2 "github.com/devsy-org/devsy/pkg/options"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/devsy-org/devsy/pkg/util"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// UpCmd holds the up cmd flags.
type UpCmd struct {
	provider2.CLIOptions
	*flags.GlobalFlags

	Machine string

	ProviderOptions []string

	ConfigureSSH       bool
	GPGAgentForwarding bool
	OpenIDE            bool
	Reconfigure        bool

	SSHConfigPath string

	DotfilesSource        string
	DotfilesScript        string
	DotfilesScriptEnv     []string // Key=Value to pass to install script
	DotfilesScriptEnvFile []string // Paths to files containing Key=Value pairs to pass to install script
}

// NewUpCmd creates a new up command.
func NewUpCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &UpCmd{GlobalFlags: f}
	upCmd := &cobra.Command{
		Use:   "up [flags] [workspace-path|workspace-name]",
		Short: "Starts a new workspace",
		RunE:  cmd.execute,
	}
	cmd.registerFlags(upCmd)
	return upCmd
}

func (cmd *UpCmd) execute(cobraCmd *cobra.Command, args []string) error {
	if err := cmd.validate(); err != nil {
		return err
	}
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}
	if devsyConfig.ContextOption(config.ContextOptionSSHStrictHostKeyChecking) == config.BoolTrue {
		cmd.StrictHostKeyChecking = true
	}

	ctx, cancel := WithSignals(cobraCmd.Context())
	defer cancel()

	client, err := cmd.prepareClient(ctx, devsyConfig, args)
	if err != nil {
		return fmt.Errorf("prepare workspace client: %w", err)
	}
	if cmd.ExtraDevContainerPath != "" && client.Provider() != "docker" {
		return fmt.Errorf("extra devcontainer file is only supported with local provider")
	}

	telemetry.CollectorCLI.SetClient(client)
	return cmd.Run(ctx, devsyConfig, client, args)
}

func (cmd *UpCmd) validate() error {
	if err := validatePodmanFlags(cmd); err != nil {
		return err
	}
	if cmd.ExtraDevContainerPath != "" {
		absPath, err := filepath.Abs(cmd.ExtraDevContainerPath)
		if err != nil {
			return err
		}
		cmd.ExtraDevContainerPath = absPath
	}
	return nil
}

func (cmd *UpCmd) registerFlags(upCmd *cobra.Command) {
	cmd.registerSSHFlags(upCmd)
	cmd.registerDotfilesFlags(upCmd)
	cmd.registerDevContainerFlags(upCmd)
	cmd.registerIDEFlags(upCmd)
	cmd.registerGitFlags(upCmd)
	cmd.registerPodmanFlags(upCmd)
	cmd.registerWorkspaceFlags(upCmd)
	cmd.registerTestingFlags(upCmd)
}

func (cmd *UpCmd) registerSSHFlags(upCmd *cobra.Command) {
	upCmd.Flags().
		BoolVar(&cmd.ConfigureSSH, "configure-ssh", true,
			"If true will configure the ssh config to include the Devsy workspace")
	upCmd.Flags().
		BoolVar(&cmd.GPGAgentForwarding, "gpg-agent-forwarding", false,
			"If true forward the local gpg-agent to the Devsy workspace")
	upCmd.Flags().
		StringVar(&cmd.SSHConfigPath, "ssh-config", "",
			"The path to the ssh config to modify, if empty will use ~/.ssh/config")
}

func (cmd *UpCmd) registerDotfilesFlags(upCmd *cobra.Command) {
	upCmd.Flags().
		StringVar(&cmd.DotfilesSource, "dotfiles", "", "The path or url to the dotfiles to use in the container")
	upCmd.Flags().
		StringVar(&cmd.DotfilesScript, "dotfiles-script", "",
			"The path in dotfiles directory to use to install the dotfiles, if empty will try to guess")
	upCmd.Flags().
		StringSliceVar(&cmd.DotfilesScriptEnv, "dotfiles-script-env", []string{},
			"Extra environment variables to put into the dotfiles install script, e.g. MY_ENV_VAR=MY_VALUE")
	upCmd.Flags().
		StringSliceVar(&cmd.DotfilesScriptEnvFile, "dotfiles-script-env-file", []string{},
			"The path to files containing environment variables to set for the dotfiles install script")
}

func (cmd *UpCmd) registerDevContainerFlags(upCmd *cobra.Command) {
	upCmd.Flags().
		StringVar(&cmd.DevContainerImage, "devcontainer-image", "",
			"The container image to use, this will override the devcontainer.json value in the project")
	upCmd.Flags().
		StringVar(&cmd.DevContainerPath, "devcontainer-path", "", "The path to the devcontainer.json relative to the project")
	upCmd.Flags().
		StringVar(&cmd.DevContainerID, "devcontainer-id", "",
			"The ID of the devcontainer to use when multiple exist "+
				"(e.g., folder name in .devcontainer/FOLDER/devcontainer.json)")
	upCmd.Flags().
		StringVar(&cmd.ExtraDevContainerPath, "extra-devcontainer-path", "",
			"The path to an additional devcontainer.json file to override original devcontainer.json")
	upCmd.Flags().
		StringVar(&cmd.FallbackImage, "fallback-image", "",
			"The fallback image to use if no devcontainer configuration has been detected")
	upCmd.Flags().
		StringVar(&cmd.AdditionalFeatures, "additional-features", "",
			`Additional features to apply to the dev container (JSON as per "features" section in devcontainer.json)`)
}

func (cmd *UpCmd) registerIDEFlags(upCmd *cobra.Command) {
	upCmd.Flags().
		StringVar(&cmd.IDE, "ide", "", "The IDE to open the workspace in. If empty will use vscode locally or in browser")
	upCmd.Flags().
		StringArrayVar(&cmd.IDEOptions, "ide-option", []string{}, "IDE option in the form KEY=VALUE")
	upCmd.Flags().
		BoolVar(&cmd.OpenIDE, "open-ide", true,
			"If this is false and an IDE is configured, Devsy will only install the IDE server backend, but not open it")
}

func (cmd *UpCmd) registerGitFlags(upCmd *cobra.Command) {
	upCmd.Flags().
		Var(&cmd.GitCloneStrategy, "git-clone-strategy",
			"The git clone strategy Devsy uses to checkout git based workspaces. "+
				"Can be full (default), blobless, treeless or shallow")
	upCmd.Flags().
		BoolVar(&cmd.GitCloneRecursiveSubmodules, "git-clone-recursive-submodules", false,
			"If true will clone git submodule repositories recursively")
	upCmd.Flags().
		StringVar(&cmd.GitSSHSigningKey, "git-ssh-signing-key", "",
			"The ssh key to use when signing git commits. Used to explicitly setup Devsy's ssh signature "+
				"forwarding with given key. Should be same format as value of `git config user.signingkey`")
}

func (cmd *UpCmd) registerPodmanFlags(upCmd *cobra.Command) {
	upCmd.Flags().
		StringVar(&cmd.Userns, "userns", "",
			"User namespace to use for the container (Podman only; e.g. \"keep-id\", \"host\", or \"auto\")")
	upCmd.Flags().
		StringSliceVar(&cmd.UidMap, "uidmap", []string{},
			"UID mapping for Podman user namespace "+
				"(Podman only; format: container_id:host_id:amount, e.g. \"0:1000:1\")")
	upCmd.Flags().
		StringSliceVar(&cmd.GidMap, "gidmap", []string{},
			"GID mapping for Podman user namespace "+
				"(Podman only; format: container_id:host_id:amount, e.g. \"0:1000:1\")")
}

func (cmd *UpCmd) registerWorkspaceFlags(upCmd *cobra.Command) {
	upCmd.Flags().StringVar(&cmd.ID, "id", "", "The id to use for the workspace")
	upCmd.Flags().
		StringVar(&cmd.Machine, "machine", "",
			"The machine to use for this workspace. The machine needs to exist beforehand or the "+
				"command will fail. If the workspace already exists, this option has no effect")
	upCmd.Flags().
		StringVar(&cmd.Source, "source", "", "Optional source for the workspace, e.g. git:https://github.com/my-org/my-repo")
	upCmd.Flags().
		StringArrayVar(&cmd.ProviderOptions, "provider-option", []string{}, "Provider option in the form KEY=VALUE")
	upCmd.Flags().
		BoolVar(&cmd.Reconfigure, "reconfigure", false,
			"Reconfigure the options for this workspace. Only supported in Devsy Pro right now.")
	upCmd.Flags().
		BoolVar(&cmd.Recreate, "recreate", false, "If true will remove any existing containers and recreate them")
	upCmd.Flags().
		BoolVar(&cmd.Reset, "reset", false,
			"If true will remove any existing containers including sources, and recreate them")
	upCmd.Flags().
		StringSliceVar(&cmd.PrebuildRepositories, "prebuild-repository", []string{},
			"Docker repository that hosts devsy prebuilds for this workspace")
	upCmd.Flags().
		StringArrayVar(&cmd.WorkspaceEnv, "workspace-env", []string{},
			"Extra env variables to put into the workspace, e.g. MY_ENV_VAR=MY_VALUE")
	upCmd.Flags().
		StringSliceVar(&cmd.WorkspaceEnvFile, "workspace-env-file", []string{},
			"The path to files containing a list of extra env variables to put into the workspace, "+
				"e.g. MY_ENV_VAR=MY_VALUE")
	upCmd.Flags().
		StringArrayVar(&cmd.InitEnv, "init-env", []string{},
			"Extra env variables to inject during the initialization of the workspace, e.g. MY_ENV_VAR=MY_VALUE")
	upCmd.Flags().
		BoolVar(&cmd.DisableDaemon, "disable-daemon", false,
			"If enabled, will not install a daemon into the target machine to track activity")
}

func (cmd *UpCmd) registerTestingFlags(upCmd *cobra.Command) {
	upCmd.Flags().StringVar(&cmd.DaemonInterval, "daemon-interval", "", "TESTING ONLY")
	_ = upCmd.Flags().MarkHidden("daemon-interval")
	upCmd.Flags().BoolVar(&cmd.ForceDockerless, "force-dockerless", false, "TESTING ONLY")
	_ = upCmd.Flags().MarkHidden("force-dockerless")
}

// Run runs the command logic.
func (cmd *UpCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
	args []string,
) error {
	cmd.prepareWorkspace(client)

	wctx, err := cmd.executeDevsyUp(ctx, devsyConfig, client)
	if err != nil {
		return err
	}
	if wctx == nil {
		return nil // Platform mode
	}

	if err := cmd.configureWorkspace(devsyConfig, client, wctx); err != nil {
		return err
	}

	return cmd.openIDE(ctx, devsyConfig, client, wctx)
}

// workspaceContext holds the result of workspace preparation.
type workspaceContext struct {
	result  *config2.Result
	user    string
	workdir string
}

// prepareWorkspace handles initial setup and validation.
func (cmd *UpCmd) prepareWorkspace(client client2.BaseWorkspaceClient) {
	if cmd.Reset {
		cmd.Recreate = true
	}

	targetIDE := client.WorkspaceConfig().IDE.Name
	if cmd.IDE != "" {
		targetIDE = cmd.IDE
	}

	if !cmd.Platform.Enabled && ide.ReusesAuthSock(targetIDE) {
		cmd.SSHAuthSockID = util.RandStringBytes(10)
		devsylog.Debug("Reusing SSH_AUTH_SOCK", cmd.SSHAuthSockID)
	} else if cmd.Platform.Enabled && ide.ReusesAuthSock(targetIDE) {
		devsylog.Debug(
			"Reusing SSH_AUTH_SOCK is not supported with platform mode, consider launching the IDE from the platform UI",
		)
	}
}

// executeDevsyUp runs the agent and returns workspace context.
func (cmd *UpCmd) executeDevsyUp(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
) (*workspaceContext, error) {
	result, err := cmd.devsyUp(ctx, devsyConfig, client)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("did not receive a result back from agent")
	}
	if cmd.Platform.Enabled {
		return nil, nil
	}

	user := config2.GetRemoteUser(result)
	workdir := ""
	if result.MergedConfig != nil && result.MergedConfig.WorkspaceFolder != "" {
		workdir = result.MergedConfig.WorkspaceFolder
	}
	if client.WorkspaceConfig().Source.GitSubPath != "" {
		result.SubstitutionContext.ContainerWorkspaceFolder = filepath.Join(
			result.SubstitutionContext.ContainerWorkspaceFolder,
			client.WorkspaceConfig().Source.GitSubPath,
		)
		workdir = result.SubstitutionContext.ContainerWorkspaceFolder
	}

	return &workspaceContext{result: result, user: user, workdir: workdir}, nil
}

// configureWorkspace sets up SSH, Git, and dotfiles.
func (cmd *UpCmd) configureWorkspace(
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
	wctx *workspaceContext,
) error {
	if cmd.ConfigureSSH {
		devsyHome := ""
		if envDevsyHome, ok := os.LookupEnv(config.EnvHome); ok {
			devsyHome = envDevsyHome
		}
		setupGPGAgentForwarding := cmd.GPGAgentForwarding ||
			devsyConfig.ContextOption(config.ContextOptionGPGAgentForwarding) == config.BoolTrue
		sshConfigIncludePath := devsyConfig.ContextOption(config.ContextOptionSSHConfigIncludePath)

		if err := configureSSH(client, configureSSHParams{
			sshConfigPath:        cmd.SSHConfigPath,
			sshConfigIncludePath: sshConfigIncludePath,
			user:                 wctx.user,
			workdir:              wctx.workdir,
			gpgagent:             setupGPGAgentForwarding,
			devsyHome:            devsyHome,
		}); err != nil {
			return err
		}

		devsylog.Info("SSH configuration completed in workspace")
	}

	if err := dotfiles.Setup(dotfiles.SetupParams{
		Source:       cmd.DotfilesSource,
		Script:       cmd.DotfilesScript,
		EnvFiles:     cmd.DotfilesScriptEnvFile,
		EnvKeyValues: cmd.DotfilesScriptEnv,
		Client:       client,
		DevsyConfig:  devsyConfig,
	}); err != nil {
		return err
	}

	return nil
}

// openIDE opens the configured IDE.
func (cmd *UpCmd) openIDE(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
	wctx *workspaceContext,
) error {
	if !cmd.OpenIDE {
		return nil
	}

	ideConfig := client.WorkspaceConfig().IDE
	return opener.Open(ctx, ideConfig.Name, ideConfig.Options, opener.Params{
		GPGAgentForwarding: cmd.GPGAgentForwarding,
		SSHAuthSockID:      cmd.SSHAuthSockID,
		GitSSHSigningKey:   cmd.GitSSHSigningKey,
		DevsyConfig:        devsyConfig,
		Client:             client,
		User:               wctx.user,
		Result:             wctx.result,
	})
}

func (cmd *UpCmd) devsyUp(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
) (*config2.Result, error) {
	var err error

	// only lock if we are not in platform mode
	if !cmd.Platform.Enabled {
		err := client.Lock(ctx)
		if err != nil {
			return nil, err
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

func (cmd *UpCmd) devsyUpProxy(
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
		defer devsylog.Debug("done executing up command")
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

func (cmd *UpCmd) devsyUpMachine(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.WorkspaceClient,
) (*config2.Result, error) {
	err := clientimplementation.StartWait(ctx, client, true)
	if err != nil {
		return nil, err
	}

	// compress info
	workspaceInfo, wInfo, err := client.AgentInfo(cmd.CLIOptions)
	if err != nil {
		return nil, err
	}

	// create container etc.
	devsylog.Info("creating devcontainer")
	defer devsylog.Debug("done creating devcontainer")

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
	if devsylog.DebugEnabled() {
		sshTunnelCmd += " --debug"
	}

	// create agent command
	agentCommand := fmt.Sprintf(
		"'%s' agent workspace up --workspace-info '%s'",
		client.AgentPath(),
		workspaceInfo,
	)

	if devsylog.DebugEnabled() {
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

type configureSSHParams struct {
	sshConfigPath        string
	sshConfigIncludePath string
	user                 string
	workdir              string
	gpgagent             bool
	devsyHome            string
}

func configureSSH(client client2.BaseWorkspaceClient, params configureSSHParams) error {
	path, err := devssh.ResolveSSHConfigPath(params.sshConfigPath)
	if err != nil {
		return fmt.Errorf("invalid ssh config path: %w", err)
	}
	sshConfigPath := path

	sshConfigIncludePath := params.sshConfigIncludePath
	if sshConfigIncludePath != "" {
		includePath, err := devssh.ResolveSSHConfigPath(sshConfigIncludePath)
		if err != nil {
			return fmt.Errorf("invalid ssh config include path: %w", err)
		}
		sshConfigIncludePath = includePath
	}

	err = devssh.ConfigureSSHConfig(devssh.SSHConfigParams{
		SSHConfigPath:        sshConfigPath,
		SSHConfigIncludePath: sshConfigIncludePath,
		Context:              client.Context(),
		Workspace:            client.Workspace(),
		User:                 params.user,
		Workdir:              params.workdir,
		GPGAgent:             params.gpgagent,
		DevsyHome:            params.devsyHome,
		Provider:             client.Provider(),
	})
	if err != nil {
		return err
	}

	return nil
}

func mergeDevsyUpOptions(baseOptions *provider2.CLIOptions) error {
	oldOptions := *baseOptions
	found, err := clientimplementation.DecodeOptionsFromEnv(
		config.EnvFlagsUp,
		baseOptions,
	)
	if err != nil {
		return fmt.Errorf("decode up options: %w", err)
	} else if found {
		baseOptions.WorkspaceEnv = append(oldOptions.WorkspaceEnv, baseOptions.WorkspaceEnv...)
		baseOptions.InitEnv = append(oldOptions.InitEnv, baseOptions.InitEnv...)
		baseOptions.PrebuildRepositories = append(
			oldOptions.PrebuildRepositories,
			baseOptions.PrebuildRepositories...)
		baseOptions.IDEOptions = append(oldOptions.IDEOptions, baseOptions.IDEOptions...)
	}

	err = clientimplementation.DecodePlatformOptionsFromEnv(&baseOptions.Platform)
	if err != nil {
		return fmt.Errorf("decode platform options: %w", err)
	}

	return nil
}

func mergeEnvFromFiles(baseOptions *provider2.CLIOptions) error {
	var variables []string
	for _, file := range baseOptions.WorkspaceEnvFile {
		envFromFile, err := config2.ParseKeyValueFile(file)
		if err != nil {
			return err
		}
		variables = append(variables, envFromFile...)
	}
	baseOptions.WorkspaceEnv = append(baseOptions.WorkspaceEnv, variables...)

	return nil
}

var inheritedEnvironmentVariables = []string{
	"GIT_AUTHOR_NAME",
	"GIT_AUTHOR_EMAIL",
	"GIT_AUTHOR_DATE",
	"GIT_COMMITTER_NAME",
	"GIT_COMMITTER_EMAIL",
	"GIT_COMMITTER_DATE",
}

func (cmd *UpCmd) prepareClient(
	ctx context.Context,
	devsyConfig *config.Config,
	args []string,
) (client2.BaseWorkspaceClient, error) {
	// try to parse flags from env
	if err := mergeDevsyUpOptions(&cmd.CLIOptions); err != nil {
		return nil, err
	}

	if cmd.Platform.Enabled {
		devsylog.Debug("Running in platform mode")
		devsylog.Debug("Using error output stream")

		// merge context options from env
		config.MergeContextOptions(devsyConfig.Current(), os.Environ())
	}

	if err := mergeEnvFromFiles(&cmd.CLIOptions); err != nil {
		return nil, err
	}

	cmd.WorkspaceEnv = options2.InheritFromEnvironment(
		cmd.WorkspaceEnv,
		inheritedEnvironmentVariables,
		"",
	)

	var source *provider2.WorkspaceSource
	if cmd.Source != "" {
		source = provider2.ParseWorkspaceSource(cmd.Source)
		if source == nil {
			return nil, fmt.Errorf("workspace source is missing")
		} else if source.LocalFolder != "" && cmd.Platform.Enabled {
			return nil, fmt.Errorf("local folder is not supported in platform mode. " +
				"Please specify a Git repository instead")
		}
	}

	if cmd.SSHConfigPath == "" {
		cmd.SSHConfigPath = devsyConfig.ContextOption(config.ContextOptionSSHConfigPath)
	}
	sshConfigIncludePath := devsyConfig.ContextOption(config.ContextOptionSSHConfigIncludePath)

	client, err := workspace2.Resolve(
		ctx,
		devsyConfig,
		workspace2.ResolveParams{
			IDE:                  cmd.IDE,
			IDEOptions:           cmd.IDEOptions,
			Args:                 args,
			DesiredID:            cmd.ID,
			DesiredMachine:       cmd.Machine,
			ProviderUserOptions:  cmd.ProviderOptions,
			ReconfigureProvider:  cmd.Reconfigure,
			DevContainerImage:    cmd.DevContainerImage,
			DevContainerPath:     cmd.DevContainerPath,
			SSHConfigPath:        cmd.SSHConfigPath,
			SSHConfigIncludePath: sshConfigIncludePath,
			Source:               source,
			UID:                  cmd.UID,
			ChangeLastUsed:       true,
			Owner:                cmd.Owner,
		},
	)
	if err != nil {
		return nil, err
	}

	if !cmd.Platform.Enabled {
		proInstance := workspace2.GetProInstance(devsyConfig, client.Provider())
		err = workspace2.CheckProviderUpdate(devsyConfig, proInstance)
		if err != nil {
			return nil, err
		}
	}

	return client, nil
}

func WithSignals(ctx context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		select {
		case <-signals:
			cancel()
		case <-ctx.Done():
		}
	}()

	go func() {
		<-ctx.Done()
		<-signals
		// force shutdown if context is done and we receive another signal
		os.Exit(1)
	}()

	return ctx, func() {
		cancel()
		signal.Stop(signals)
	}
}

func validatePodmanFlags(cmd *UpCmd) error {
	if cmd.Userns != "" && (len(cmd.UidMap) > 0 || len(cmd.GidMap) > 0) {
		return fmt.Errorf(
			"--userns cannot be combined with --uidmap or --gidmap (mutually exclusive)",
		)
	}
	for _, m := range cmd.UidMap {
		if !isValidMapping(m) {
			return fmt.Errorf(
				"invalid --uidmap format: %s (expected: container_id:host_id:amount)",
				m,
			)
		}
	}
	for _, m := range cmd.GidMap {
		if !isValidMapping(m) {
			return fmt.Errorf(
				"invalid --gidmap format: %s (expected: container_id:host_id:amount)",
				m,
			)
		}
	}
	return nil
}

func isValidMapping(mapping string) bool {
	parts := strings.Split(mapping, ":")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}
