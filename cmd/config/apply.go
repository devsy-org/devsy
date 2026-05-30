package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/workspace"
	"github.com/devsy-org/devsy/pkg/copy"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/spf13/cobra"
)

const (
	flagApplyContainer       = "container"
	flagApplyConfig          = "config"
	flagApplyWorkspaceFolder = "workspace-folder"
	flagApplyDockerPath      = "docker-path"

	hookOnCreate      = "onCreateCommand"
	hookUpdateContent = "updateContentCommand"
	hookPostCreate    = "postCreateCommand"
	hookPostStart     = "postStartCommand"
	hookPostAttach    = "postAttachCommand"
)

// ApplyCmd holds the 'config apply' command flags.
type ApplyCmd struct {
	*flags.GlobalFlags

	Container       string
	Config          string
	WorkspaceFolder string
	DockerPath      string
}

// NewApplyCmd creates a new 'config apply' command.
func NewApplyCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &ApplyCmd{GlobalFlags: f}
	applyCmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply devcontainer configuration to a running container",
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	applyCmd.Flags().StringVar(&cmd.Container, flagApplyContainer, "",
		"The container ID or name to apply configuration to (required)")
	_ = applyCmd.MarkFlagRequired(flagApplyContainer)
	applyCmd.Flags().StringVar(&cmd.Config, flagApplyConfig, "",
		"Path to devcontainer.json (defaults to auto-detection in current workspace)")
	applyCmd.Flags().StringVar(&cmd.WorkspaceFolder, flagApplyWorkspaceFolder, "",
		"Workspace folder path inside the container")
	applyCmd.Flags().StringVar(&cmd.DockerPath, flagApplyDockerPath, "",
		"Path to the docker/podman executable (defaults to 'docker')")

	return applyCmd
}

// Run executes the 'config apply' command logic.
func (cmd *ApplyCmd) Run(ctx context.Context) error {
	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	emitJSON := mode == output.ModeJSON

	helper := &docker.DockerHelper{DockerCommand: cmd.resolveDockerPath()}

	containerDetails, result, err := cmd.prepareContainer(ctx, helper, emitJSON)
	if err != nil {
		return err
	}

	workdir := cmd.resolveWorkdir(containerDetails, result)
	envArgs := workspace.BuildLifecycleEnvArgs(result)
	envArgs = append(envArgs, buildContainerEnvArgs(result.MergedConfig.ContainerEnv)...)

	if err := cmd.installFeatures(ctx, helper, result); err != nil {
		emitErr(emitJSON, err)
		return fmt.Errorf("feature installation: %w", err)
	}

	params := &workspace.LifecycleExecParams{
		Ctx:         ctx,
		Helper:      helper,
		ContainerID: containerDetails.ID,
		EnvArgs:     envArgs,
		Workdir:     workdir,
		User:        devcconfig.GetRemoteUser(result),
	}

	if err := cmd.runApplyLifecycleHooks(params, result); err != nil {
		emitErr(emitJSON, err)
		return err
	}

	log.Infof("apply completed for container %s", containerDetails.ID)
	if emitJSON {
		_ = devcconfig.WriteResultJSON(os.Stderr, devcconfig.ResultEnvelope{
			ContainerID:           containerDetails.ID,
			RemoteUser:            devcconfig.GetRemoteUser(result),
			RemoteWorkspaceFolder: workdir,
		})
	}
	return nil
}

func (cmd *ApplyCmd) prepareContainer(
	ctx context.Context,
	helper *docker.DockerHelper,
	emitJSON bool,
) (*devcconfig.ContainerDetails, *devcconfig.Result, error) {
	containerDetails, err := cmd.inspectRunningContainer(ctx, helper)
	if err != nil {
		emitErr(emitJSON, err)
		return nil, nil, err
	}

	result, err := cmd.loadConfig(containerDetails)
	if err != nil {
		emitErr(emitJSON, err)
		return nil, nil, err
	}
	return containerDetails, result, nil
}

func emitErr(emitJSON bool, err error) {
	if emitJSON {
		_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
	}
}

func (cmd *ApplyCmd) resolveDockerPath() string {
	if cmd.DockerPath != "" {
		return cmd.DockerPath
	}
	return workspace.DefaultDockerCommand
}

func (cmd *ApplyCmd) inspectRunningContainer(
	ctx context.Context,
	helper *docker.DockerHelper,
) (*devcconfig.ContainerDetails, error) {
	details, err := helper.InspectContainers(ctx, []string{cmd.Container})
	if err != nil {
		return nil, fmt.Errorf("inspect container %s: %w", cmd.Container, err)
	}
	if len(details) == 0 {
		return nil, fmt.Errorf("container %s not found", cmd.Container)
	}

	containerDetails := &details[0]
	if !strings.EqualFold(containerDetails.State.Status, workspace.ContainerStatusRunning) {
		return nil, fmt.Errorf(
			"container %s is not running (status: %s)",
			cmd.Container,
			containerDetails.State.Status,
		)
	}
	return containerDetails, nil
}

func (cmd *ApplyCmd) loadConfig(
	containerDetails *devcconfig.ContainerDetails,
) (*devcconfig.Result, error) {
	var devContainerConfig *devcconfig.DevContainerConfig
	var err error

	if cmd.Config != "" {
		devContainerConfig, err = devcconfig.ParseDevContainerJSONFile(cmd.Config)
	} else {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return nil, fmt.Errorf("get working directory: %w", cwdErr)
		}
		devContainerConfig, err = devcconfig.ParseDevContainerJSON(cwd, "")
	}
	if err != nil {
		return nil, fmt.Errorf("parse devcontainer config: %w", err)
	}
	if devContainerConfig == nil {
		return nil, errors.New("no devcontainer configuration found")
	}

	mergedConfig, err := devcconfig.MergeConfiguration(devContainerConfig, nil)
	if err != nil {
		return nil, fmt.Errorf("merge configuration: %w", err)
	}

	return &devcconfig.Result{
		MergedConfig:     mergedConfig,
		ContainerDetails: containerDetails,
	}, nil
}

func (cmd *ApplyCmd) resolveWorkdir(
	containerDetails *devcconfig.ContainerDetails,
	result *devcconfig.Result,
) string {
	if cmd.WorkspaceFolder != "" {
		return cmd.WorkspaceFolder
	}
	if result.MergedConfig.WorkspaceFolder != "" {
		return result.MergedConfig.WorkspaceFolder
	}
	return containerDetails.Config.WorkingDir
}

func (cmd *ApplyCmd) installFeatures(
	ctx context.Context,
	helper *docker.DockerHelper,
	result *devcconfig.Result,
) error {
	if len(result.MergedConfig.Features) == 0 {
		return nil
	}

	featureSets, err := cmd.resolveFeatureSets(result)
	if err != nil {
		return err
	}
	if len(featureSets) == 0 {
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "devsy-features-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	featureStageDir := filepath.Join(tmpDir, "features")
	// #nosec G301 -- features need to be executable inside the container
	if err := os.MkdirAll(featureStageDir, 0o750); err != nil {
		return fmt.Errorf("create features staging dir: %w", err)
	}

	remoteUser := devcconfig.GetRemoteUser(result)
	if err := cmd.stageFeatures(featureSets, featureStageDir, remoteUser); err != nil {
		return err
	}

	if err := cmd.copyAndExecFeatures(ctx, helper, featureSets, featureStageDir); err != nil {
		return err
	}

	return nil
}

func (cmd *ApplyCmd) resolveFeatureSets(
	result *devcconfig.Result,
) ([]*devcconfig.FeatureSet, error) {
	devContainerConfig := &devcconfig.DevContainerConfig{}
	devContainerConfig.Features = result.MergedConfig.Features
	devContainerConfig.OverrideFeatureInstallOrder = result.MergedConfig.OverrideFeatureInstallOrder
	devContainerConfig.Origin = result.MergedConfig.Origin

	featureSets, err := feature.ResolveFeatureOrder(devContainerConfig)
	if err != nil {
		return nil, fmt.Errorf("resolve features: %w", err)
	}
	return featureSets, nil
}

func (cmd *ApplyCmd) copyAndExecFeatures(
	ctx context.Context,
	helper *docker.DockerHelper,
	featureSets []*devcconfig.FeatureSet,
	featureStageDir string,
) error {
	containerFeaturesPath := "/tmp/build-features"

	cpArgs := []string{"cp", featureStageDir + "/.", cmd.Container + ":" + containerFeaturesPath}
	if err := helper.Run(ctx, cpArgs, nil, os.Stdout, os.Stderr); err != nil {
		return fmt.Errorf("copy features to container: %w", err)
	}

	for i, fs := range featureSets {
		log.Infof("installing feature: %s", fs.ConfigID)
		installCmd := fmt.Sprintf(
			"cd %s/%d && chmod +x ./devcontainer-features-install.sh && ./devcontainer-features-install.sh",
			containerFeaturesPath,
			i,
		)
		execArgs := workspace.BuildDockerExecArgs(workspace.DockerExecArgs{
			Container: cmd.Container,
			Command:   []string{installCmd},
		})
		if err := helper.Run(ctx, execArgs, os.Stdin, os.Stdout, os.Stderr); err != nil {
			return fmt.Errorf("install feature %s: %w", fs.ConfigID, err)
		}
	}

	return nil
}

func (cmd *ApplyCmd) stageFeatures(
	featureSets []*devcconfig.FeatureSet,
	stageDir string,
	remoteUser string,
) error {
	builtinEnvContent := fmt.Sprintf(
		"_CONTAINER_USER=%s\n_REMOTE_USER=%s\n",
		remoteUser,
		remoteUser,
	)
	builtinEnvPath := filepath.Join(stageDir, "devcontainer-features.builtin.env")
	if err := os.WriteFile(builtinEnvPath, []byte(builtinEnvContent), 0o600); err != nil {
		return fmt.Errorf("write builtin env: %w", err)
	}

	for i, fs := range featureSets {
		featureDir := filepath.Join(stageDir, fmt.Sprintf("%d", i))
		// #nosec G301 -- feature dirs need to be traversable for docker cp
		if err := os.MkdirAll(featureDir, 0o750); err != nil {
			return fmt.Errorf("create feature dir %d: %w", i, err)
		}

		if err := copy.Directory(fs.Folder, featureDir); err != nil {
			return fmt.Errorf("copy feature %s: %w", fs.ConfigID, err)
		}

		envVars := feature.GetFeatureEnvVariables(fs.Config, fs.Options)
		envPath := filepath.Join(featureDir, "devcontainer-features.env")
		if err := os.WriteFile(envPath, []byte(strings.Join(envVars, "\n")), 0o600); err != nil {
			return fmt.Errorf("write env for feature %s: %w", fs.ConfigID, err)
		}

		installWrapper := feature.GetFeatureInstallWrapperScript(fs.ConfigID, fs.Config, envVars)
		wrapperPath := filepath.Join(featureDir, "devcontainer-features-install.sh")
		// #nosec G306 -- install scripts must be executable
		if err := os.WriteFile(wrapperPath, []byte(installWrapper), 0o600); err != nil {
			return fmt.Errorf("write install wrapper for feature %s: %w", fs.ConfigID, err)
		}
	}

	return nil
}

func (cmd *ApplyCmd) runApplyLifecycleHooks(
	params *workspace.LifecycleExecParams,
	result *devcconfig.Result,
) error {
	hooks := []struct {
		name string
		cmds []types.LifecycleHook
	}{
		{hookOnCreate, result.MergedConfig.OnCreateCommands},
		{hookUpdateContent, result.MergedConfig.UpdateContentCommands},
		{hookPostCreate, result.MergedConfig.PostCreateCommands},
		{hookPostStart, result.MergedConfig.PostStartCommands},
		{hookPostAttach, result.MergedConfig.PostAttachCommands},
	}

	for _, hook := range hooks {
		for _, h := range hook.cmds {
			if err := workspace.ExecLifecycleHook(params, hook.name, h); err != nil {
				return fmt.Errorf("lifecycle hooks: %s: %w", hook.name, err)
			}
		}
	}
	return nil
}

func buildContainerEnvArgs(containerEnv map[string]string) []string {
	if len(containerEnv) == 0 {
		return nil
	}

	keys := make([]string, 0, len(containerEnv))
	for k := range containerEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	args := make([]string, 0, len(containerEnv)*2)
	for _, k := range keys {
		args = append(args, "-e", k+"="+containerEnv[k])
	}
	return args
}
