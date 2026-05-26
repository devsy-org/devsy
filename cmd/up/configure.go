package up

import (
	"context"
	"fmt"
	"os"

	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/dotfiles"
	"github.com/devsy-org/devsy/pkg/ide/opener"
	"github.com/devsy-org/devsy/pkg/log"
	devssh "github.com/devsy-org/devsy/pkg/ssh"
)

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

		log.Info("SSH configuration completed in workspace")
	}

	// Dotfiles are now installed in-container during the lifecycle
	// (between postCreateCommand and postStartCommand). The host-side
	// SSH-based installation is only used as a fallback when the
	// container-side path was not configured.
	if cmd.DotfilesRepo == "" {
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
	return opener.Open(ctx, ideConfig.Name, ideConfig.Options, opener.IDEParams{
		GPGAgentForwarding: cmd.GPGAgentForwarding,
		SSHAuthSockID:      cmd.SSHAuthSockID,
		GitSSHSigningKey:   cmd.GitSSHSigningKey,
		DevsyConfig:        devsyConfig,
		Client:             client,
		User:               wctx.user,
		Result:             wctx.result,
		TunnelMode:         wctx.tunnelPort > 0,
	})
}

func (cmd *UpCmd) reconfigureSSHWithTunnel(
	devsyConfig *config.Config,
	client client2.BaseWorkspaceClient,
	wctx *workspaceContext,
) error {
	if !cmd.ConfigureSSH || wctx.tunnelPort == 0 {
		return nil
	}

	path, err := devssh.ResolveSSHConfigPath(cmd.SSHConfigPath)
	if err != nil {
		return err
	}

	sshConfigIncludePath := devsyConfig.ContextOption(config.ContextOptionSSHConfigIncludePath)
	if sshConfigIncludePath != "" {
		includePath, err := devssh.ResolveSSHConfigPath(sshConfigIncludePath)
		if err != nil {
			return err
		}
		sshConfigIncludePath = includePath
	}

	return devssh.ConfigureSSHConfig(devssh.SSHConfigParams{
		SSHConfigPath:        path,
		SSHConfigIncludePath: sshConfigIncludePath,
		Context:              client.Context(),
		Workspace:            client.Workspace(),
		User:                 wctx.user,
		Workdir:              wctx.workdir,
		TunnelPort:           wctx.tunnelPort,
		Provider:             client.Provider(),
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
