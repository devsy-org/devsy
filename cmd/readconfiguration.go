package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/cmd/flags"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/metadata"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/spf13/cobra"
)

// ReadConfigurationCmd holds the read-configuration cmd flags.
type ReadConfigurationCmd struct {
	*flags.GlobalFlags

	WorkspaceFolder              string
	Config                       string
	ContainerID                  string
	IDLabels                     []string
	IncludeFeaturesConfiguration bool
	IncludeMergedConfiguration   bool
}

// NewReadConfigurationCmd creates a new read-configuration command.
func NewReadConfigurationCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &ReadConfigurationCmd{GlobalFlags: f}
	readConfigCmd := &cobra.Command{
		Use:   "read-configuration",
		Short: "Reads and outputs the merged devcontainer configuration as JSON",
		RunE:  cmd.Run,
	}

	readConfigCmd.Flags().
		StringVar(
			&cmd.WorkspaceFolder,
			"workspace-folder",
			"",
			"Path to the workspace folder",
		)
	readConfigCmd.Flags().
		StringVar(
			&cmd.ContainerID,
			"container-id",
			"",
			"Read configuration from a running container with the given ID",
		)
	readConfigCmd.Flags().
		StringVar(
			&cmd.Config,
			"config",
			"",
			"Path to a specific devcontainer.json",
		)
	readConfigCmd.Flags().
		StringArrayVar(
			&cmd.IDLabels,
			"id-label",
			nil,
			"Override the default container identification labels (format: key=value, can be specified multiple times)",
		)
	readConfigCmd.Flags().
		BoolVar(
			&cmd.IncludeFeaturesConfiguration,
			"include-features-configuration",
			false,
			"Include features in the output",
		)
	readConfigCmd.Flags().
		BoolVar(
			&cmd.IncludeMergedConfiguration,
			"include-merged-configuration",
			false,
			"Include the merged configuration in the output",
		)

	return readConfigCmd
}

type readConfigurationOutput struct {
	Configuration *config2.DevContainerConfig       `json:"configuration"`
	Workspace     readConfigurationWorkspace        `json:"workspace"`
	Features      map[string]any                    `json:"features,omitempty"`
	Merged        *config2.MergedDevContainerConfig `json:"mergedConfiguration,omitempty"`
}

type readConfigurationWorkspace struct {
	Folder string `json:"workspaceFolder"`
}

// Run executes the read-configuration command.
func (cmd *ReadConfigurationCmd) Run(
	c *cobra.Command,
	_ []string,
) error {
	if err := config2.ValidateIDLabels(cmd.IDLabels); err != nil {
		return err
	}

	parsedConfig, workspaceFolder, err := cmd.resolve(c.Context())
	if err != nil {
		return err
	}

	output := readConfigurationOutput{
		Configuration: parsedConfig,
		Workspace: readConfigurationWorkspace{
			Folder: workspaceFolder,
		},
	}

	if cmd.IncludeFeaturesConfiguration {
		output.Features = parsedConfig.Features
	}

	if cmd.IncludeMergedConfiguration {
		merged, mergeErr := buildMergedConfig(parsedConfig)
		if mergeErr != nil {
			return mergeErr
		}
		output.Merged = merged
	}

	out, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal output: %w", err)
	}

	_, _ = os.Stdout.Write(out)
	_, _ = os.Stdout.WriteString("\n")

	return nil
}

func (cmd *ReadConfigurationCmd) resolve(ctx context.Context) (
	*config2.DevContainerConfig,
	string,
	error,
) {
	if cmd.ContainerID == "" && cmd.WorkspaceFolder == "" && len(cmd.IDLabels) == 0 {
		return nil, "", fmt.Errorf(
			"either --workspace-folder, --container-id, or --id-label must be provided",
		)
	}
	if cmd.ContainerID != "" {
		return cmd.resolveConfigFromContainer(ctx)
	}
	if len(cmd.IDLabels) > 0 {
		return cmd.resolveConfigFromIDLabels(ctx)
	}
	return cmd.resolveConfig()
}

func (cmd *ReadConfigurationCmd) resolveConfig() (
	*config2.DevContainerConfig,
	string,
	error,
) {
	workspaceFolder, err := filepath.Abs(cmd.WorkspaceFolder)
	if err != nil {
		return nil, "", fmt.Errorf("resolve workspace folder: %w", err)
	}

	info, err := os.Stat(workspaceFolder)
	if err != nil {
		return nil, "", fmt.Errorf(
			"workspace folder %s: %w",
			workspaceFolder,
			err,
		)
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf(
			"workspace folder %s is not a directory",
			workspaceFolder,
		)
	}

	var parsedConfig *config2.DevContainerConfig
	if cmd.Config != "" {
		parsedConfig, err = config2.ParseDevContainerJSONFile(cmd.Config)
	} else {
		parsedConfig, err = config2.ParseDevContainerJSON(
			workspaceFolder,
			"",
		)
	}
	if err != nil {
		return nil, "", fmt.Errorf("parse devcontainer config: %w", err)
	}
	if parsedConfig == nil {
		return nil, "", fmt.Errorf(
			"no devcontainer configuration found in %s",
			workspaceFolder,
		)
	}

	return parsedConfig, workspaceFolder, nil
}

func (cmd *ReadConfigurationCmd) resolveConfigFromContainer(ctx context.Context) (
	*config2.DevContainerConfig,
	string,
	error,
) {
	helper := &docker.DockerHelper{DockerCommand: defaultDockerCommand}

	details, err := helper.InspectContainers(ctx, []string{cmd.ContainerID})
	if err != nil {
		return nil, "", fmt.Errorf("inspect container %s: %w", cmd.ContainerID, err)
	}
	if len(details) == 0 {
		return nil, "", fmt.Errorf("container %s not found", cmd.ContainerID)
	}

	containerDetails := &details[0]
	subCtx := &config2.SubstitutionContext{}

	imageMetadata, err := metadata.GetImageMetadataFromContainer(
		containerDetails,
		subCtx,
	)
	if err != nil {
		return nil, "", fmt.Errorf("get image metadata from container: %w", err)
	}

	parsedConfig := &config2.DevContainerConfig{}
	if len(imageMetadata.Config) > 0 {
		last := imageMetadata.Config[len(imageMetadata.Config)-1]
		parsedConfig.DevContainerConfigBase = last.DevContainerConfigBase
		parsedConfig.DevContainerActions = last.DevContainerActions
		parsedConfig.NonComposeBase = last.NonComposeBase
	}

	workspaceFolder := containerDetails.Config.WorkingDir
	if workspaceFolder == "" {
		workspaceFolder = "/"
	}

	return parsedConfig, workspaceFolder, nil
}

func (cmd *ReadConfigurationCmd) resolveConfigFromIDLabels(ctx context.Context) (
	*config2.DevContainerConfig,
	string,
	error,
) {
	containerDetails, err := findRunningContainer(
		ctx, defaultDockerCommand, "", cmd.IDLabels,
	)
	if err != nil {
		return nil, "", err
	}

	subCtx := &config2.SubstitutionContext{}
	imageMetadata, err := metadata.GetImageMetadataFromContainer(
		containerDetails,
		subCtx,
	)
	if err != nil {
		return nil, "", fmt.Errorf("get image metadata from container: %w", err)
	}

	parsedConfig := &config2.DevContainerConfig{}
	if len(imageMetadata.Config) > 0 {
		last := imageMetadata.Config[len(imageMetadata.Config)-1]
		parsedConfig.DevContainerConfigBase = last.DevContainerConfigBase
		parsedConfig.DevContainerActions = last.DevContainerActions
		parsedConfig.NonComposeBase = last.NonComposeBase
	}

	workspaceFolder := containerDetails.Config.WorkingDir
	if workspaceFolder == "" {
		workspaceFolder = "/"
	}

	return parsedConfig, workspaceFolder, nil
}

func buildMergedConfig(
	parsedConfig *config2.DevContainerConfig,
) (*config2.MergedDevContainerConfig, error) {
	imageMetadataConfig := &config2.ImageMetadataConfig{}
	config2.AddConfigToImageMetadata(parsedConfig, imageMetadataConfig)

	mergedConfig, err := config2.MergeConfiguration(
		parsedConfig,
		imageMetadataConfig.Config,
	)
	if err != nil {
		return nil, fmt.Errorf("merge configuration: %w", err)
	}

	return mergedConfig, nil
}
