package up

import (
	"os"
	"runtime"

	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/wslg"
)

const wslgGoosWindows = "windows"

func (cmd *UpCmd) preflightWSLgContext(cfg *config.Config) error {
	if !cfg.ContextOptionBool(config.ContextOptionWSLgEnabled) {
		return nil
	}
	if runtime.GOOS != wslgGoosWindows || cmd.Platform.Enabled {
		return wslg.ErrUnsupported
	}
	return wslg.ValidateTarget(
		wslg.Target{
			Distro: cfg.ContextOption(config.ContextOptionWSLgDistro),
			User:   cfg.ContextOption(config.ContextOptionWSLgUser),
		},
	)
}

// Reject unsupported providers before Create/Start can provision a machine.
// The runner subsequently checks its actual driver before discovering values.
func (cmd *UpCmd) preflightWSLgClient(
	cfg *config.Config,
	client client2.BaseWorkspaceClient,
) error {
	if !cfg.ContextOptionBool(config.ContextOptionWSLgEnabled) {
		return nil
	}
	local, ok := client.(client2.WorkspaceClient)
	if !ok || !local.AgentLocal() {
		return wslg.ErrUnsupported
	}
	_, info, err := local.AgentInfo(cmd.CLIOptions)
	if err != nil {
		return err
	}
	if err := wslg.CheckEnvironmentSources(map[string][]string{
		"process environment":         os.Environ(),
		"--init-env":                  cmd.InitEnv,
		"provider Docker environment": wslg.Pairs(info.Agent.Docker.Env),
	}, runtime.GOOS == wslgGoosWindows); err != nil {
		return err
	}
	return validateWSLgAgent(info.Agent)
}

func validateWSLgAgent(agent provider.ProviderAgentConfig) error {
	if agent.Driver != "" && agent.Driver != provider.DockerDriver {
		return wslg.ErrUnsupported
	}
	var containerRuntime docker.ContainerRuntime
	if agent.Docker.Runtime != "" {
		var err error
		containerRuntime, err = docker.RuntimeFromName(agent.Docker.Runtime)
		if err != nil {
			return err
		}
	} else {
		command := agent.Docker.Path
		if command == "" {
			command = provider.DockerDriver
		}
		containerRuntime = docker.DetectRuntime(command)
	}
	if containerRuntime.Name() != docker.RuntimeDocker {
		return wslg.ErrUnsupported
	}
	return nil
}
