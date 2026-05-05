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

//nolint:cyclop,funlen
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
		log.Debug("Running in platform mode")
		log.Debug("Using error output stream")

		// merge context options from env
		config.MergeContextOptions(devsyConfig.Current(), os.Environ())
	}

	if err := mergeEnvFromFiles(&cmd.CLIOptions); err != nil {
		return nil, err
	}

	if cmd.SecretsFile != "" {
		parsed, err := secrets.ParseSecretsFile(cmd.SecretsFile)
		if err != nil {
			return nil, err
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
