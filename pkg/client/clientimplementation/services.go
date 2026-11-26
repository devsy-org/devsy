package clientimplementation

import (
	"context"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/credentials"
	daemon "github.com/devsy-org/devsy/pkg/daemon/platform"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/tunnel"
	"golang.org/x/crypto/ssh"
)

type StartServicesDaemonOptions struct {
	DevsyConfig      *config.Config
	Client           client.DaemonClient
	SSHClient        *ssh.Client
	User             string
	ForwardPorts     bool
	ExtraPorts       []string
	GitSSHSigningKey string
}

type credentialConfig struct {
	docker          bool
	git             bool
	github          bool
	gitSSHSignature bool
}

// StartServicesDaemon starts the services daemon for credential forwarding and port forwarding.
func StartServicesDaemon(ctx context.Context, opts StartServicesDaemonOptions) error {
	services, err := daemonServiceOptions(ctx, opts)
	if err != nil {
		return err
	}
	if opts.User == "" {
		return nil
	}
	return tunnel.RunServices(ctx, services)
}

func StartPreparedServicesDaemon(
	ctx context.Context,
	opts StartServicesDaemonOptions,
) credentials.Readiness {
	services, err := daemonServiceOptions(ctx, opts)
	if err != nil {
		log.Warn("credential services preparation unavailable")
		return credentials.Readiness{GitHubState: "unavailable"}
	}
	if opts.User == "" {
		return credentials.Readiness{GitHubState: "unavailable"}
	}
	return tunnel.StartPreparedServices(ctx, services)
}

func RunPreparedServicesDaemon(
	ctx context.Context,
	opts StartServicesDaemonOptions,
	prepared func(credentials.Readiness),
) error {
	services, err := daemonServiceOptions(ctx, opts)
	if err != nil {
		return err
	}
	if opts.User == "" {
		return nil
	}
	return tunnel.RunPreparedServices(ctx, services, prepared)
}

func daemonServiceOptions(
	ctx context.Context,
	opts StartServicesDaemonOptions,
) (tunnel.RunServicesOptions, error) {
	if opts.User == "" {
		return tunnel.RunServicesOptions{}, nil
	}

	workspace, err := getWorkspace(ctx, opts.Client)
	if err != nil {
		return tunnel.RunServicesOptions{}, err
	}

	credConfig := getCredentialConfig(opts.DevsyConfig, workspace)

	return tunnel.RunServicesOptions{
		DevsyConfig:                    opts.DevsyConfig,
		ContainerClient:                opts.SSHClient,
		User:                           opts.User,
		ForwardPorts:                   opts.ForwardPorts,
		ExtraPorts:                     opts.ExtraPorts,
		PlatformOptions:                nil,
		Workspace:                      opts.Client.WorkspaceConfig(),
		ConfigureDockerCredentials:     credConfig.docker,
		ConfigureGitCredentials:        credConfig.git,
		ConfigureGitHubCredentials:     credConfig.github,
		ConfigureGitSSHSignatureHelper: credConfig.gitSSHSignature,
		GitSSHSigningKey:               opts.GitSSHSigningKey,
	}, nil
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
		github: devsyConfig.ContextOption(
			config.ContextOptionGitHubCLIForwarding,
		) == config.BoolTrue,
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
		cfg.github = cfg.github && !instanceCredentialForwarding.Git.Disabled
		cfg.gitSSHSignature = !instanceCredentialForwarding.Git.Disabled
	}

	return cfg
}
