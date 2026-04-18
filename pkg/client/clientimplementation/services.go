package clientimplementation

import (
	"context"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	daemon "github.com/devsy-org/devsy/pkg/daemon/platform"
	"github.com/devsy-org/devsy/pkg/tunnel"
	"github.com/devsy-org/log"
	"golang.org/x/crypto/ssh"
)

type StartServicesDaemonOptions struct {
	DevsyConfig     *config.Config
	Client           client.DaemonClient
	SSHClient        *ssh.Client
	User             string
	Log              log.Logger
	ForwardPorts     bool
	ExtraPorts       []string
	GitSSHSigningKey string
}

type credentialConfig struct {
	docker          bool
	git             bool
	gitSSHSignature bool
}

// StartServicesDaemon starts the services daemon for credential forwarding and port forwarding.
func StartServicesDaemon(ctx context.Context, opts StartServicesDaemonOptions) error {
	if opts.User == "" {
		return nil
	}

	workspace, err := getWorkspace(ctx, opts.Client)
	if err != nil {
		return err
	}

	credConfig := getCredentialConfig(opts.DevsyConfig, workspace)

	return tunnel.RunServices(
		ctx,
		tunnel.RunServicesOptions{
			DevsyConfig:                   opts.DevsyConfig,
			ContainerClient:                opts.SSHClient,
			User:                           opts.User,
			ForwardPorts:                   opts.ForwardPorts,
			ExtraPorts:                     opts.ExtraPorts,
			PlatformOptions:                nil,
			Workspace:                      opts.Client.WorkspaceConfig(),
			ConfigureDockerCredentials:     credConfig.docker,
			ConfigureGitCredentials:        credConfig.git,
			ConfigureGitSSHSignatureHelper: credConfig.gitSSHSignature,
			GitSSHSigningKey:               opts.GitSSHSigningKey,
			Log:                            opts.Log,
		},
	)
}

func getWorkspace(
	ctx context.Context,
	client client.DaemonClient,
) (*managementv1.DevsyWorkspaceInstance, error) {
	return daemon.NewLocalClient(client.Provider()).GetWorkspace(ctx, client.WorkspaceConfig().UID)
}

func getCredentialConfig(
	devsyConfig *config.Config,
	workspace *managementv1.DevsyWorkspaceInstance,
) credentialConfig {
	cfg := credentialConfig{
		docker: devsyConfig.ContextOption(
			config.ContextOptionSSHInjectDockerCredentials,
		) == config.BoolTrue,
		git: devsyConfig.ContextOption(
			config.ContextOptionSSHInjectGitCredentials,
		) == config.BoolTrue,
		gitSSHSignature: devsyConfig.ContextOption(
			config.ContextOptionGitSSHSignatureForwarding,
		) == config.BoolTrue,
	}

	if workspace == nil || workspace.Status.Instance == nil {
		return cfg
	}

	instance := workspace.Status.Instance
	if instance.CredentialForwarding == nil {
		return cfg
	}

	instanceCredentialForwarding := instance.CredentialForwarding
	if instanceCredentialForwarding.Docker != nil {
		cfg.docker = !instanceCredentialForwarding.Docker.Disabled
	}
	if instanceCredentialForwarding.Git != nil {
		cfg.git = !instanceCredentialForwarding.Git.Disabled
		cfg.gitSSHSignature = !instanceCredentialForwarding.Git.Disabled
	}

	return cfg
}
