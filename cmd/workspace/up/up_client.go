package up

import (
	"context"
	"fmt"
	"os"

	client2 "github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/log"
	options2 "github.com/devsy-org/devsy/pkg/options"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/secrets"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
)

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
	if err := mergeDevsyUpOptions(&cmd.CLIOptions); err != nil {
		return nil, err
	}
	if cmd.Platform.Enabled {
		log.Debug("Running in platform mode")
		log.Debug("Using error output stream")
		config.MergeContextOptions(devsyConfig.Current(), os.Environ())
	}
	if err := cmd.prepareSecrets(); err != nil {
		return nil, err
	}
	source, err := cmd.parseWorkspaceSource()
	if err != nil {
		return nil, err
	}
	cmd.resolveSSHConfig(devsyConfig)

	log.Debugf("up: resolving workspace with cmd.IDE=%q ide-launch=%q", cmd.IDE, cmd.IDELaunch)
	client, err := workspace2.Resolve(
		ctx,
		devsyConfig,
		cmd.resolveParams(args, source, devsyConfig),
	)
	if err != nil {
		return nil, err
	}
	if !cmd.Platform.Enabled {
		proInstance := workspace2.GetProInstance(devsyConfig, client.Provider())
		if err := workspace2.CheckProviderUpdate(devsyConfig, proInstance); err != nil {
			return nil, err
		}
	}
	return client, nil
}

func (cmd *UpCmd) resolveParams(
	args []string, source *provider2.WorkspaceSource, devsyConfig *config.Config,
) workspace2.ResolveParams {
	return workspace2.ResolveParams{
		IDE:                 cmd.IDE,
		IDEOptions:          cmd.IDEOptions,
		Args:                args,
		DesiredID:           cmd.ID,
		DesiredMachine:      cmd.Machine,
		ProviderUserOptions: cmd.ProviderOptions,
		ReconfigureProvider: cmd.Reconfigure,
		DevContainerImage:   cmd.DevContainerImage,
		DevContainerPath:    cmd.DevContainerPath,
		SSHConfigPath:       cmd.SSHConfigPath,
		SSHConfigIncludePath: devsyConfig.ContextOption(
			config.ContextOptionSSHConfigIncludePath,
		),
		Source:         source,
		UID:            cmd.UID,
		ChangeLastUsed: true,
		Owner:          cmd.Owner,
	}
}

func (cmd *UpCmd) prepareSecrets() error {
	if err := mergeEnvFromFiles(&cmd.CLIOptions); err != nil {
		return err
	}

	if cmd.SecretsFile != "" {
		parsed, err := secrets.ParseSecretsFile(cmd.SecretsFile)
		if err != nil {
			return err
		}
		for k, v := range parsed {
			cmd.SecretsEnv = append(cmd.SecretsEnv, k+"="+v)
		}
	}

	if cmd.FeatureSecretsFile == "" {
		cmd.FeatureSecretsFile = os.Getenv("DEVCONTAINER_SECRETS_FILE")
	}
	if cmd.FeatureSecretsFile != "" {
		cmd.CLIOptions.FeatureSecretsFile = cmd.FeatureSecretsFile
	}

	cmd.WorkspaceEnv = options2.InheritFromEnvironment(
		cmd.WorkspaceEnv,
		inheritedEnvironmentVariables,
		"",
	)

	return nil
}

func (cmd *UpCmd) parseWorkspaceSource() (*provider2.WorkspaceSource, error) {
	if cmd.Source == "" {
		return nil, nil
	}

	source := provider2.ParseWorkspaceSource(cmd.Source)
	if source == nil {
		return nil, fmt.Errorf("workspace source is missing")
	}
	if source.LocalFolder != "" && cmd.Platform.Enabled {
		return nil, fmt.Errorf("local folder is not supported in platform mode. " +
			"Specify a Git repository instead")
	}

	return source, nil
}

func (cmd *UpCmd) resolveSSHConfig(devsyConfig *config.Config) {
	if cmd.SSHConfigPath == "" {
		cmd.SSHConfigPath = devsyConfig.ContextOption(config.ContextOptionSSHConfigPath)
	}
}
