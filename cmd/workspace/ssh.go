package workspace

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"al.essio.dev/pkg/shellescape"
	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/machine"
	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/gpg"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/port"
	"github.com/devsy-org/devsy/pkg/provider"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
	"github.com/devsy-org/devsy/pkg/tunnel"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
)

const (
	DisableSSHKeepAlive time.Duration = 0 * time.Second
)

// SSHCmd holds the ssh cmd flags.
type SSHCmd struct {
	*flags.GlobalFlags

	ForwardPortsTimeout string
	ForwardPorts        []string
	ReverseForwardPorts []string
	SendEnvVars         []string
	SetEnvVars          []string

	Stdio                     bool
	JumpContainer             bool
	ReuseSSHAuthSock          string
	AgentForwarding           bool
	GPGAgentForwarding        bool
	GitSSHSignatureForwarding bool
	GitSSHSigningKey          string

	// ssh keepalive options
	SSHKeepAliveInterval time.Duration `json:"sshKeepAliveInterval,omitempty"`

	StartServices   bool
	TermMode        string
	InstallTerminfo bool

	Command string
	User    string
	WorkDir string
}

// NewSSHCmd creates a new ssh command.
func NewSSHCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &SSHCmd{
		GlobalFlags: f,
	}
	sshCmd := &cobra.Command{
		Use:   "ssh [flags] [workspace-folder|workspace-name]",
		Short: "Open an SSH session to a workspace",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.execute(cobraCmd.Context(), args)
		},
		ValidArgsFunction: func(rootCmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
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

	cliflags.Add(
		sshCmd,
		cliflags.StringArray(
			&cmd.ForwardPorts,
			names.ForwardPorts,
			nil,
			"Specifies that connections to the given TCP port or Unix socket on the local (client) "+
				"host are to be forwarded to the given host and port, or Unix socket, on the remote side.",
		).
			Shorthand("L"),
		cliflags.StringArray(
			&cmd.ReverseForwardPorts,
			names.ReverseForwardPorts,
			nil,
			"Specifies that connections to the given TCP port or Unix socket on the local (client) "+
				"host are to be reverse forwarded to the given host and port, or Unix socket, on the remote side.",
		).
			Shorthand("R"),
		cliflags.StringArray(&cmd.SendEnvVars, names.SendEnv, nil,
			"Specifies which local env variables shall be sent to the container."),
		cliflags.StringArray(&cmd.SetEnvVars, names.SetEnv, nil,
			"Specifies env variables to be set in the container."),
		cliflags.String(
			&cmd.ForwardPortsTimeout,
			names.ForwardPortsTimeout,
			"",
			"Specifies the timeout after which the command should terminate when the ports are unused.",
		),
		cliflags.String(
			&cmd.Command,
			names.Command,
			"",
			"The command to execute within the workspace",
		),
		cliflags.String(&cmd.User, names.User, "", "The user of the workspace to use"),
		cliflags.String(&cmd.WorkDir, names.Workdir, "", "The working directory in the container"),
		cliflags.Bool(&cmd.AgentForwarding, names.AgentForwarding, true,
			"If true forward the local ssh keys to the remote machine"),
		cliflags.String(&cmd.ReuseSSHAuthSock, names.ReuseSSHAuthSock, "",
			"If set, the SSH_AUTH_SOCK is expected to already be available in the workspace "+
				"(under /tmp using the key provided) and the connection reuses this instead of creating a new one").
			Hidden(),
		cliflags.Bool(&cmd.GPGAgentForwarding, names.SSHGPGForwarding, false,
			"Forward the local gpg-agent to the remote machine"),
		cliflags.Bool(
			&cmd.Stdio,
			names.Stdio,
			false,
			"If true will tunnel connection through stdout and stdin",
		),
		cliflags.Bool(&cmd.StartServices, names.StartServices, true,
			"If false will not start any port-forwarding or git / docker credentials helper"),
		cliflags.Duration(&cmd.SSHKeepAliveInterval, names.SSHKeepAliveInterval, 55*time.Second,
			"How often should keepalive request be made (55s)"),
		cliflags.String(&cmd.GitSSHSigningKey, names.GitSSHSigningKey, "",
			"The SSH signing key to use for git commit signing inside the workspace"),
		cliflags.String(&cmd.TermMode, names.TermMode, machine.TermModeAuto,
			"PTY TERM selection mode: auto, strict, fallback"),
		cliflags.Bool(&cmd.InstallTerminfo, names.InstallTerminfo, false,
			"Install local TERM terminfo on remote before PTY"),
	)

	return sshCmd
}

// Run runs the command logic.
func (cmd *SSHCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
) error {
	cmd.addPrivateKeysToAgentIfEnabled(ctx, devsyConfig)

	// get user
	if cmd.User == "" {
		var err error
		cmd.User, err = devssh.GetUser(
			client.WorkspaceConfig().ID,
			client.WorkspaceConfig().SSHConfigPath,
			client.WorkspaceConfig().SSHConfigIncludePath,
		)
		if err != nil {
			return err
		}
	}

	// set default context if needed
	if cmd.Context == "" {
		cmd.Context = devsyConfig.DefaultContext
	}

	workspaceClient, ok := client.(client2.WorkspaceClient)
	if ok {
		return cmd.jumpContainer(ctx, devsyConfig, workspaceClient)
	}
	proxyClient, ok := client.(client2.ProxyClient)
	if ok {
		return cmd.startProxyTunnel(ctx, devsyConfig, proxyClient)
	}
	daemonClient, ok := client.(client2.DaemonClient)
	if ok {
		return cmd.jumpContainerTailscale(ctx, devsyConfig, daemonClient)
	}

	return nil
}

func (cmd *SSHCmd) addPrivateKeysToAgentIfEnabled(ctx context.Context, devsyConfig *config.Config) {
	if devsyConfig.ContextOption(config.ContextOptionSSHAgentForwarding) != config.BoolTrue ||
		devsyConfig.ContextOption(config.ContextOptionSSHAddPrivateKeys) != config.BoolTrue {
		return
	}
	log.Debug(
		"adding ssh keys to agent, disable via 'devsy context set -o SSH_ADD_PRIVATE_KEYS=false'",
	)
	if err := devssh.AddPrivateKeysToAgent(ctx); err != nil {
		log.Debugf("Error adding private keys to ssh-agent: %v", err)
	}
}

func (cmd *SSHCmd) execute(ctx context.Context, args []string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}
	client, err := workspace2.Get(ctx, workspace2.GetOptions{
		DevsyConfig:    devsyConfig,
		Args:           args,
		ChangeLastUsed: true,
		Owner:          cmd.Owner,
		LocalOnly:      cmd.Stdio,
	})
	if err != nil {
		return err
	}
	return cmd.Run(ctx, devsyConfig, client)
}

func (cmd *SSHCmd) jumpContainerTailscale(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.DaemonClient,
) error {
	log.Debugf("Starting tailscale connection")

	err := client.CheckWorkspaceReachable(ctx)
	if err != nil {
		return err
	}

	toolSSHClient, sshClient, err := client.SSHClients(ctx, cmd.User)
	if err != nil {
		return err
	}
	defer func() { _ = toolSSHClient.Close() }()
	defer func() { _ = sshClient.Close() }()

	// Forward or reverse-forward ports if specified
	if handled, err := cmd.forwardPortsIfRequested(ctx, toolSSHClient); handled {
		return err
	}

	cmd.startServicesDaemon(ctx, devsyConfig, client, toolSSHClient)

	// Handle GPG agent forwarding
	if err := cmd.maybeSetupGPGAgent(ctx, devsyConfig, toolSSHClient); err != nil {
		return err
	}

	// Handle ssh stdio mode
	if cmd.Stdio {
		if cmd.SSHKeepAliveInterval != DisableSSHKeepAlive {
			go startSSHKeepAlive(ctx, toolSSHClient, cmd.SSHKeepAliveInterval)
		}

		return client.DirectTunnel(ctx, os.Stdin, os.Stdout)
	}

	// Connect to the inner server and handle user session
	return machine.RunSSHSession(
		ctx,
		sshClient,
		machine.RunSSHSessionOptions{
			AgentForwarding: cmd.AgentForwarding,
			Command:         cmd.Command,
			SessionOptions: machine.SSHSessionOptions{
				TermMode:        cmd.TermMode,
				InstallTerminfo: cmd.InstallTerminfo,
			},
			Stderr: os.Stderr,
		},
	)
}

// forwardPortsIfRequested handles -L/-R forwarding when requested. The returned
// bool reports whether forwarding took over (the caller should return err).
func (cmd *SSHCmd) forwardPortsIfRequested(
	ctx context.Context,
	sshClient *ssh.Client,
) (bool, error) {
	if len(cmd.ForwardPorts) > 0 {
		return true, cmd.forwardPorts(ctx, sshClient)
	}
	if len(cmd.ReverseForwardPorts) > 0 && !cmd.GPGAgentForwarding {
		return true, cmd.reverseForwardPorts(ctx, sshClient)
	}
	return false, nil
}

func (cmd *SSHCmd) startServicesDaemon(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.DaemonClient,
	sshClient *ssh.Client,
) {
	if !cmd.StartServices {
		return
	}
	go func() {
		err := clientimplementation.StartServicesDaemon(
			ctx,
			clientimplementation.StartServicesDaemonOptions{
				DevsyConfig:  devsyConfig,
				Client:       client,
				SSHClient:    sshClient,
				User:         cmd.User,
				ForwardPorts: false,
				ExtraPorts:   nil,
			},
		)
		if err != nil {
			log.Errorf("Error starting services: %v", err)
		}
	}()
}

func (cmd *SSHCmd) maybeSetupGPGAgent(
	ctx context.Context,
	devsyConfig *config.Config,
	sshClient *ssh.Client,
) error {
	if !cmd.GPGAgentForwarding &&
		!devsyConfig.ContextOptionBool(config.ContextOptionGPGAgentForwarding) {
		return nil
	}
	if gpg.IsGpgTunnelRunning(ctx, cmd.User, sshClient) {
		log.Debugf("[GPG] exporting already running, skipping")
		return nil
	}
	return cmd.setupGPGAgent(ctx, sshClient)
}

func (cmd *SSHCmd) startProxyTunnel(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.ProxyClient,
) error {
	log.Debugf("Start proxy tunnel")
	return tunnel.NewTunnel(
		ctx,
		func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
			return client.Ssh(ctx, client2.SshOptions{
				User:   cmd.User,
				Stdin:  stdin,
				Stdout: stdout,
			})
		},
		func(ctx context.Context, containerClient *ssh.Client) error {
			return cmd.startTunnel(ctx, devsyConfig, containerClient, client)
		},
	)
}

func (cmd *SSHCmd) retrieveEnVars() (map[string]string, error) {
	envVars := make(map[string]string)
	for _, envVar := range cmd.SendEnvVars {
		envVarValue, exist := os.LookupEnv(envVar)
		if exist {
			envVars[envVar] = envVarValue
		}
	}
	for _, envVar := range cmd.SetEnvVars {
		parts := strings.Split(envVar, "=")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid env var: %s", envVar)
		}
		envVars[parts[0]] = parts[1]
	}

	return envVars, nil
}

func (cmd *SSHCmd) jumpContainer(
	ctx context.Context,
	devsyConfig *config.Config,
	client client2.WorkspaceClient,
) error {
	// lock the workspace as long as we init the connection
	err := client.Lock(ctx)
	if err != nil {
		return err
	}
	defer client.Unlock()

	// start the workspace
	err = clientimplementation.StartWait(ctx, client, false)
	if err != nil {
		return err
	}

	envVars, err := cmd.retrieveEnVars()
	if err != nil {
		return err
	}

	// tunnel to container
	return tunnel.NewContainerTunnel(client).
		Run(ctx, func(ctx context.Context, containerClient *ssh.Client) error {
			// we have a connection to the container, make sure others can connect as well
			client.Unlock()

			// start ssh tunnel
			return cmd.startTunnel(ctx, devsyConfig, containerClient, client)
		}, devsyConfig, envVars)
}

func (cmd *SSHCmd) forwardTimeout() (time.Duration, error) {
	timeout := time.Duration(0)
	if cmd.ForwardPortsTimeout != "" {
		timeout, err := time.ParseDuration(cmd.ForwardPortsTimeout)
		if err != nil {
			return timeout, fmt.Errorf("parse forward ports timeout: %w", err)
		}

		log.Infof("Using port forwarding timeout of %s", cmd.ForwardPortsTimeout)
	}

	return timeout, nil
}

func (cmd *SSHCmd) reverseForwardPorts(
	ctx context.Context,
	containerClient *ssh.Client,
) error {
	timeout, err := cmd.forwardTimeout()
	if err != nil {
		return fmt.Errorf("parse forward ports timeout: %w", err)
	}

	errChan := make(chan error, len(cmd.ReverseForwardPorts))
	for _, portMapping := range cmd.ReverseForwardPorts {
		mapping, err := port.ParsePortSpec(portMapping)
		if err != nil {
			return fmt.Errorf("parse port mapping: %w", err)
		}

		// start the forwarding
		log.Infof(
			"Reverse forwarding local %s/%s to remote %s/%s",
			mapping.Host.Protocol,
			mapping.Host.Address,
			mapping.Container.Protocol,
			mapping.Container.Address,
		)
		go func(portMapping string) {
			err := devssh.ReversePortForward(
				ctx,
				containerClient,
				mapping.Host.Protocol,
				mapping.Host.Address,
				mapping.Container.Protocol,
				mapping.Container.Address,
				timeout,
			)
			if errors.Is(err, devssh.ErrIdleTimeout) {
				log.Infof("reverse port-forward %s exited due to idle timeout", portMapping)
				errChan <- nil
				return
			}
			if !errors.Is(err, io.EOF) {
				errChan <- fmt.Errorf("error forwarding %s: %w", portMapping, err)
			}
		}(portMapping)
	}

	return <-errChan
}

func (cmd *SSHCmd) forwardPorts(
	ctx context.Context,
	containerClient *ssh.Client,
) error {
	timeout, err := cmd.forwardTimeout()
	if err != nil {
		return fmt.Errorf("parse forward ports timeout: %w", err)
	}

	errChan := make(chan error, len(cmd.ForwardPorts))
	for _, portMapping := range cmd.ForwardPorts {
		mapping, err := port.ParsePortSpec(portMapping)
		if err != nil {
			return fmt.Errorf("parse port mapping: %w", err)
		}

		// start the forwarding
		log.Infof(
			"Forwarding local %s/%s to remote %s/%s",
			mapping.Host.Protocol,
			mapping.Host.Address,
			mapping.Container.Protocol,
			mapping.Container.Address,
		)
		go func(portMapping string) {
			err := devssh.PortForward(
				ctx,
				containerClient,
				mapping.Host.Protocol,
				mapping.Host.Address,
				mapping.Container.Protocol,
				mapping.Container.Address,
				timeout,
			)
			if errors.Is(err, devssh.ErrIdleTimeout) {
				log.Infof("port-forward %s exited due to idle timeout", portMapping)
				errChan <- nil
				return
			}
			if !errors.Is(err, io.EOF) {
				errChan <- fmt.Errorf("error forwarding %s: %w", portMapping, err)
			}
		}(portMapping)
	}

	return <-errChan
}

func (cmd *SSHCmd) startTunnel(
	ctx context.Context,
	devsyConfig *config.Config,
	containerClient *ssh.Client,
	workspaceClient client2.BaseWorkspaceClient,
) error {
	// check if we should forward ports
	if handled, err := cmd.forwardPortsIfRequested(ctx, containerClient); handled {
		return err
	}

	cmd.startTunnelServices(ctx, devsyConfig, containerClient, workspaceClient)

	// start ssh
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()

	// check if we should do gpg agent forwarding
	if err := cmd.maybeSetupGPGAgent(ctx, devsyConfig, containerClient); err != nil {
		return err
	}

	workdir := resolveWorkdir(cmd.WorkDir, workspaceClient)

	log.Debugf("Run outer container tunnel")
	command := cmd.buildSSHServerCommand(workdir)

	envVars, err := cmd.retrieveEnVars()
	if err != nil {
		return err
	}

	// Traffic is coming in from the outside, we need to forward it to the container
	if cmd.Stdio {
		return devssh.Run(ctx, devssh.RunOptions{
			Client:  containerClient,
			Command: command,
			Stdin:   os.Stdin,
			Stdout:  os.Stdout,
			Stderr:  writer,
			EnvVars: envVars,
		})
	}

	return machine.StartSSHSession(ctx, machine.StartSSHSessionOptions{
		User:    cmd.User,
		Command: cmd.Command,
		AgentForwarding: cmd.AgentForwarding &&
			devsyConfig.ContextOption(config.ContextOptionSSHAgentForwarding) == config.BoolTrue,
		SessionOptions: machine.SSHSessionOptions{
			TermMode:        cmd.TermMode,
			InstallTerminfo: cmd.InstallTerminfo,
		},
		Exec: func(ctx context.Context, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
			if cmd.SSHKeepAliveInterval != DisableSSHKeepAlive {
				go startSSHKeepAlive(ctx, containerClient, cmd.SSHKeepAliveInterval)
			}
			return devssh.Run(ctx, devssh.RunOptions{
				Client:  containerClient,
				Command: command,
				Stdin:   stdin,
				Stdout:  stdout,
				Stderr:  stderr,
				EnvVars: envVars,
			})
		},
		Stderr: writer,
	})
}

func (cmd *SSHCmd) startTunnelServices(
	ctx context.Context,
	devsyConfig *config.Config,
	containerClient *ssh.Client,
	workspaceClient client2.BaseWorkspaceClient,
) {
	if !cmd.StartServices {
		return
	}
	configureDockerCredentials := devsyConfig.ContextOption(
		config.ContextOptionSSHInjectDockerCredentials,
	) == config.BoolTrue
	configureGitCredentials := devsyConfig.ContextOption(
		config.ContextOptionSSHInjectGitCredentials,
	) == config.BoolTrue
	configureGitSSHSignatureHelper := devsyConfig.ContextOption(
		config.ContextOptionGitSSHSignatureForwarding,
	) == config.BoolTrue

	go cmd.startServices(
		ctx,
		devsyConfig,
		containerClient,
		workspaceClient.WorkspaceConfig(),
		configureDockerCredentials,
		configureGitCredentials,
		configureGitSSHSignatureHelper,
		cmd.GitSSHSigningKey,
	)
}

func (cmd *SSHCmd) buildSSHServerCommand(workdir string) string {
	commandArgs := []string{
		config.ContainerDevsyHelperLocation,
		"internal",
		"ssh-server",
		names.Flag(names.TrackActivity),
		names.Flag(names.Stdio),
		names.Flag(names.Workdir),
		workdir,
	}
	if cmd.ReuseSSHAuthSock != "" {
		log.Debug("Reusing SSH_AUTH_SOCK")
		commandArgs = append(
			commandArgs,
			names.Flag(names.ReuseSSHAuthSock),
			cmd.ReuseSSHAuthSock,
		)
	}
	if cmd.Debug {
		commandArgs = append(commandArgs, names.Flag(names.Debug))
	}
	command := shellescape.QuoteCommand(commandArgs)
	if cmd.User != "" && cmd.User != "root" {
		command = shellescape.QuoteCommand([]string{"su", "-c", command, cmd.User})
	}
	return command
}

func resolveWorkdir(
	workdir string,
	workspaceClient client2.BaseWorkspaceClient,
) string {
	if workdir != "" {
		return workdir
	}

	if workspaceFolder := resolveMergedWorkspaceFolder(
		workspaceClient,
	); workspaceFolder != "" {
		return workspaceFolder
	}

	return path.Join("/workspaces", workspaceClient.Workspace())
}

func resolveMergedWorkspaceFolder(
	workspaceClient client2.BaseWorkspaceClient,
) string {
	workspaceConfig := workspaceClient.WorkspaceConfig()
	if workspaceConfig == nil || workspaceConfig.Context == "" || workspaceConfig.ID == "" {
		return ""
	}

	result, err := provider.LoadWorkspaceResult(workspaceConfig.Context, workspaceConfig.ID)
	if err != nil {
		log.Warnf("Error loading workspace result for workdir resolution: %v", err)
		return ""
	}
	if result == nil || result.MergedConfig == nil {
		return ""
	}

	return result.MergedConfig.WorkspaceFolder
}

func (cmd *SSHCmd) startServices(
	ctx context.Context,
	devsyConfig *config.Config,
	containerClient *ssh.Client,
	workspace *provider.Workspace,
	configureDockerCredentials, configureGitCredentials, configureGitSSHSignatureHelper bool,
	gitSSHSigningKey string,
) {
	if cmd.User != "" {
		err := tunnel.RunServices(
			ctx,
			tunnel.RunServicesOptions{
				DevsyConfig:                    devsyConfig,
				ContainerClient:                containerClient,
				User:                           cmd.User,
				ForwardPorts:                   false,
				ExtraPorts:                     nil,
				PlatformOptions:                nil,
				Workspace:                      workspace,
				ConfigureDockerCredentials:     configureDockerCredentials,
				ConfigureGitCredentials:        configureGitCredentials,
				ConfigureGitSSHSignatureHelper: configureGitSSHSignatureHelper,
				GitSSHSigningKey:               gitSSHSigningKey,
			},
		)
		if err != nil {
			log.Debugf("Error running credential server: %v", err)
		}
	}
}

// setupGPGAgent will forward a local gpg-agent into the remote container
// this works by using cmd/internal/agentworkspace/setup_gpg.
func (cmd *SSHCmd) setupGPGAgent(
	ctx context.Context,
	containerClient *ssh.Client,
) error {
	log.Debugf("[GPG] exporting gpg owner trust from host")
	ownerTrustExport, err := gpg.GetHostOwnerTrust()
	if err != nil {
		return fmt.Errorf("export local ownertrust from GPG: %w", err)
	}
	ownerTrustArgument := base64.StdEncoding.EncodeToString(ownerTrustExport)

	log.Debugf("[GPG] detecting gpg-agent socket path on host")
	// Detect local agent extra socket, this will be forwarded to the remote and
	// symlinked in multiple paths
	gpgExtraSocketPath, err := gpg.DetectAgentSocketPath()
	if err != nil {
		return err
	}
	log.Debugf("[GPG] detected gpg-agent socket path %s", gpgExtraSocketPath)

	gitKey := gpg.SigningKey(ctx)

	cmd.ReverseForwardPorts = append(cmd.ReverseForwardPorts, gpgExtraSocketPath)

	// Now we forward the agent socket to the remote, and setup remote gpg to use it
	forwardAgent := []string{
		config.ContainerDevsyHelperLocation,
		"internal",
		"agent",
		"workspace",
		"setup-gpg",
		names.Flag(names.OwnerTrust),
		ownerTrustArgument,
		names.Flag(names.SocketPath),
		gpgExtraSocketPath,
	}

	if log.DebugEnabled() {
		forwardAgent = append(forwardAgent, names.Flag(names.Debug))
	}

	if gitKey != "" {
		forwardAgent = append(forwardAgent, names.Flag(names.GitKey))
		forwardAgent = append(forwardAgent, gitKey)
	}

	command := shellescape.QuoteCommand(forwardAgent)
	if cmd.User != "" && cmd.User != "root" {
		command = shellescape.QuoteCommand([]string{"su", "-c", command, cmd.User})
	}

	log.Debugf(
		"[GPG] start reverse forward of gpg-agent socket %s, keeping connection open",
		gpgExtraSocketPath,
	)

	go func() {
		log.Error(cmd.reverseForwardPorts(ctx, containerClient))
	}()

	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()
	err = devssh.Run(ctx, devssh.RunOptions{
		Client:  containerClient,
		Command: command,
		Stdout:  writer,
		Stderr:  writer,
	})
	if err != nil {
		return fmt.Errorf("run gpg agent setup command: %w", err)
	}

	return nil
}

func startSSHKeepAlive(
	ctx context.Context,
	client *ssh.Client,
	interval time.Duration,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				log.Errorf("Failed to send keepalive: %v", err)
			}
		}
	}
}
