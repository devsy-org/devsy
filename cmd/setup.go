package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
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
	flagSetUpContainer       = "container"
	flagSetUpConfig          = "config"
	flagSetUpWorkspaceFolder = "workspace-folder"
	flagSetUpDockerPath      = "docker-path"
	dockerExecSubcommand     = "exec"
)

// SetUpCmd holds the set-up command flags.
type SetUpCmd struct {
	*flags.GlobalFlags

	Container       string
	Config          string
	WorkspaceFolder string
	DockerPath      string
}

// NewSetUpCmd creates a new set-up command.
func NewSetUpCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &SetUpCmd{GlobalFlags: f}
	setupCmd := &cobra.Command{
		Use:   "set-up",
		Short: "Apply devcontainer configuration to a running container",
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	setupCmd.Flags().StringVar(&cmd.Container, flagSetUpContainer, "",
		"The container ID or name to apply configuration to (required)")
	_ = setupCmd.MarkFlagRequired(flagSetUpContainer)
	setupCmd.Flags().StringVar(&cmd.Config, flagSetUpConfig, "",
		"Path to devcontainer.json (defaults to auto-detection in current workspace)")
	setupCmd.Flags().StringVar(&cmd.WorkspaceFolder, flagSetUpWorkspaceFolder, "",
		"Workspace folder path inside the container")
	setupCmd.Flags().StringVar(&cmd.DockerPath, flagSetUpDockerPath, "",
		"Path to the docker/podman executable (defaults to 'docker')")

	return setupCmd
}

// Run executes the set-up command logic.
func (cmd *SetUpCmd) Run(ctx context.Context) error {
	emitJSON := output.ResolveMode(cmd.ResultFormat) == output.ModeJSON

	helper := &docker.DockerHelper{DockerCommand: cmd.resolveDockerPath()}

	containerDetails, err := cmd.inspectRunningContainer(ctx, helper)
	if err != nil {
		if emitJSON {
			_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
		}
		return err
	}

	result, err := cmd.loadConfig(containerDetails)
	if err != nil {
		if emitJSON {
			_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
		}
		return err
	}

	workdir := cmd.resolveWorkdir(containerDetails, result)
	envArgs := buildLifecycleEnvArgs(result)
	envArgs = append(envArgs, buildContainerEnvArgs(result.MergedConfig.ContainerEnv)...)

	if err := cmd.installFeatures(ctx, helper, result); err != nil {
		if emitJSON {
			_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
		}
		return fmt.Errorf("feature installation: %w", err)
	}

	params := &lifecycleExecParams{
		ctx:         ctx,
		helper:      helper,
		containerID: containerDetails.ID,
		envArgs:     envArgs,
		workdir:     workdir,
	}

	if err := cmd.runSetUpLifecycleHooks(params, result); err != nil {
		if emitJSON {
			_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
		}
		return err
	}

	user := devcconfig.GetRemoteUser(result)
	log.Infof("set-up completed for container %s", containerDetails.ID)
	if emitJSON {
		_ = devcconfig.WriteResultJSON(os.Stderr, containerDetails.ID, user, workdir, nil)
	}
	return nil
}

func (cmd *SetUpCmd) resolveDockerPath() string {
	if cmd.DockerPath != "" {
		return cmd.DockerPath
	}
	return defaultDockerCommand
}

func (cmd *SetUpCmd) inspectRunningContainer(
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
	if !strings.EqualFold(containerDetails.State.Status, containerStatusRunning) {
		return nil, fmt.Errorf(
			"container %s is not running (status: %s)",
			cmd.Container,
			containerDetails.State.Status,
		)
	}
	return containerDetails, nil
}

func (cmd *SetUpCmd) loadConfig(
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

func (cmd *SetUpCmd) resolveWorkdir(
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

func (cmd *SetUpCmd) installFeatures(
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

func (cmd *SetUpCmd) resolveFeatureSets(
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

func (cmd *SetUpCmd) copyAndExecFeatures(
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
		execArgs := buildDockerExecArgs(cmd.Container, nil, "", []string{installCmd})
		if err := helper.Run(ctx, execArgs, os.Stdin, os.Stdout, os.Stderr); err != nil {
			return fmt.Errorf("install feature %s: %w", fs.ConfigID, err)
		}
	}

	return nil
}

func (cmd *SetUpCmd) stageFeatures(
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

func (cmd *SetUpCmd) runSetUpLifecycleHooks(
	params *lifecycleExecParams,
	result *devcconfig.Result,
) error {
	hooks := []struct {
		name string
		cmds []types.LifecycleHook
	}{
		{"onCreateCommand", result.MergedConfig.OnCreateCommands},
		{"updateContentCommand", result.MergedConfig.UpdateContentCommands},
		{"postCreateCommand", result.MergedConfig.PostCreateCommands},
		{"postStartCommand", result.MergedConfig.PostStartCommands},
		{"postAttachCommand", result.MergedConfig.PostAttachCommands},
	}

	for _, hook := range hooks {
		for _, h := range hook.cmds {
			if err := execLifecycleHook(params, hook.name, h); err != nil {
				return fmt.Errorf("lifecycle hooks: %s: %w", hook.name, err)
			}
		}
	}
	return nil
}

func buildDockerExecArgs(
	container string,
	envArgs []string,
	workspaceFolder string,
	command []string,
) []string {
	args := []string{dockerExecSubcommand}
	args = append(args, envArgs...)
	if workspaceFolder != "" {
		args = append(args, "--workdir", workspaceFolder)
	}
	args = append(args, container)
	if len(command) == 1 {
		args = append(args, "sh", "-c", command[0])
	} else {
		args = append(args, command...)
	}
	return args
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
