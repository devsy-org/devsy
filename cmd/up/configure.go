package up

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

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

	if isNewContainer(wctx) {
		if err := waitForSSHReady(ctx, client, wctx.user); err != nil {
			log.Warnf("SSH readiness probe failed: %v", err)
		}
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

const containerNewThreshold = 30 * time.Second

func isNewContainer(wctx *workspaceContext) bool {
	if wctx.result == nil || wctx.result.ContainerDetails == nil {
		return false
	}
	created, err := time.Parse(time.RFC3339Nano, wctx.result.ContainerDetails.Created)
	if err != nil {
		return false
	}
	return time.Since(created) < containerNewThreshold
}

const (
	sshProbeTimeout  = 10 * time.Second
	sshProbeInterval = 500 * time.Millisecond
)

// waitForSSHReady polls until the workspace SSH server accepts connections,
// using the same transport path that VS Code Remote SSH will use.
func waitForSSHReady(ctx context.Context, client client2.BaseWorkspaceClient, user string) error {
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, sshProbeTimeout)
	defer cancel()

	workspace := client.Workspace()
	log.Debugf("probing SSH readiness for workspace %s", workspace)

	ticker := time.NewTicker(sshProbeInterval)
	defer ticker.Stop()

	for {
		if err := runSSHProbe(ctx, execPath, workspace, user); err == nil {
			log.Debugf("SSH ready for workspace %s", workspace)
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("workspace %s SSH not ready within %s", workspace, sshProbeTimeout)
		case <-ticker.C:
		}
	}
}

func runSSHProbe(ctx context.Context, execPath, workspace, user string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	args := []string{"ssh", "--command", "true", workspace}
	if user != "" {
		args = []string{"ssh", "--command", "true", "--user", user, workspace}
	}

	//nolint:gosec // execPath is from os.Executable()
	cmd := exec.CommandContext(probeCtx, execPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
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
