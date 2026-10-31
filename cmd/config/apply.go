package config

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/types"
	pkgworkspace "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/devsy-org/devsy/pkg/workspacelogs"
	"github.com/spf13/cobra"
)

const (
	hookOnCreate      = "onCreateCommand"
	hookUpdateContent = "updateContentCommand"
	hookPostCreate    = "postCreateCommand"
	hookPostStart     = "postStartCommand"
	hookPostAttach    = "postAttachCommand"
)

type ApplyCmd struct {
	*flags.GlobalFlags

	Container       string
	Config          string
	WorkspaceFolder string
	DockerPath      string
}

func NewApplyCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &ApplyCmd{GlobalFlags: f}
	applyCmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply devcontainer configuration to a running container",
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	cliflags.Add(applyCmd,
		cliflags.String(&cmd.Container, names.Container, "",
			"The container ID or name to apply configuration to (required)"),
		cliflags.String(&cmd.Config, names.Config, "",
			"Path to devcontainer.json (defaults to auto-detection in current workspace)"),
		cliflags.String(&cmd.WorkspaceFolder, names.WorkspaceFolder, "",
			"Workspace folder path inside the container"),
		cliflags.String(&cmd.DockerPath, names.DockerPath, "",
			"Path to the docker/podman executable (defaults to 'docker')"),
	)
	_ = applyCmd.MarkFlagRequired(names.Container)

	return applyCmd
}

func (cmd *ApplyCmd) Run(ctx context.Context) error {
	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	emitJSON := mode == output.ModeJSON
	reporter, err := newStatusReporter(
		cmd.ResultFormat,
		os.Stdout,
		cmd.Verbosity > 0 || cmd.Debug,
		cmd.NoStatus,
	)
	if err != nil {
		return err
	}

	helper := &docker.DockerHelper{DockerCommand: cmd.resolveDockerPath()}

	containerDetails, result, err := cmd.resolveContainer(ctx, reporter, helper)
	if err != nil {
		return err
	}

	workdir := cmd.resolveWorkdir(containerDetails, result)
	envArgs := workspace.BuildLifecycleEnvArgs(result)
	envArgs = append(envArgs, buildContainerEnvArgs(result.MergedConfig.ContainerEnv)...)

	if err := status.Run(
		ctx,
		reporter,
		status.Operation{Phase: status.PhaseRunningLifecycleHook, Step: "install features"},
		cmd.installFeaturesStep(helper, result, emitJSON),
	); err != nil {
		return err
	}

	params := &workspace.LifecycleExecParams{
		Ctx:         ctx,
		Helper:      helper,
		ContainerID: containerDetails.ID,
		EnvArgs:     envArgs,
		Workdir:     workdir,
		User:        devcconfig.GetRemoteUser(result),
		Recorder:    workspacelogs.RecorderFrom(ctx),
	}

	if err := cmd.runLifecycle(ctx, reporter, params, result); err != nil {
		return err
	}

	log.Debugf("apply completed for container %s", containerDetails.ID)
	if emitJSON {
		return devcconfig.WriteResultJSON(os.Stdout, devcconfig.ResultEnvelope{
			ContainerID:           containerDetails.ID,
			RemoteUser:            devcconfig.GetRemoteUser(result),
			RemoteWorkspaceFolder: workdir,
		})
	}
	return nil
}

func (cmd *ApplyCmd) runLifecycle(
	ctx context.Context,
	reporter status.Reporter,
	params *workspace.LifecycleExecParams,
	result *devcconfig.Result,
) error {
	if err := status.Run(
		ctx,
		reporter,
		status.Operation{Phase: status.PhaseRunningLifecycleHook},
		func(context.Context) error {
			return cmd.runApplyLifecycleHooks(params, result)
		},
	); err != nil {
		return err
	}
	return status.Run(
		ctx,
		reporter,
		status.Operation{Phase: status.PhaseReady},
		func(context.Context) error {
			return nil
		},
	)
}

func (cmd *ApplyCmd) resolveContainer(
	ctx context.Context,
	reporter status.Reporter,
	helper *docker.DockerHelper,
) (*devcconfig.ContainerDetails, *devcconfig.Result, error) {
	var containerDetails *devcconfig.ContainerDetails
	var result *devcconfig.Result
	err := status.Run(
		ctx,
		reporter,
		status.Operation{Phase: status.PhaseResolvingConfig},
		func(ctx context.Context) error {
			var resolveErr error
			containerDetails, result, resolveErr = cmd.prepareContainer(ctx, helper)
			return resolveErr
		},
	)
	return containerDetails, result, err
}

func (cmd *ApplyCmd) installFeaturesStep(
	helper *docker.DockerHelper,
	result *devcconfig.Result,
	emitJSON bool,
) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := cmd.installFeatures(ctx, helper, result, emitJSON); err != nil {
			return fmt.Errorf("feature installation: %w", err)
		}
		return nil
	}
}

func (cmd *ApplyCmd) prepareContainer(
	ctx context.Context,
	helper *docker.DockerHelper,
) (*devcconfig.ContainerDetails, *devcconfig.Result, error) {
	containerDetails, err := cmd.inspectRunningContainer(ctx, helper)
	if err != nil {
		return nil, nil, err
	}

	result, err := cmd.loadConfig(ctx, containerDetails)
	if err != nil {
		return nil, nil, err
	}
	return containerDetails, result, nil
}

func newStatusReporter(
	resultFormat string,
	out io.Writer,
	verbose bool,
	noStatus bool,
) (status.Reporter, error) {
	return status.NewCLIReporter(status.CLIOptions{
		Format:   resultFormat,
		Out:      out,
		Prefix:   "config",
		Verbose:  verbose,
		NoStatus: noStatus,
		Pipeline: status.PipelineWorkspaceUp,
		Labels: map[status.Phase]string{
			status.PhaseResolvingConfig:      "resolving devcontainer config",
			status.PhaseRunningLifecycleHook: "applying lifecycle configuration",
			status.PhaseReady:                "ready",
		},
		Envelope: func(e status.Event) error {
			return devcconfig.WriteStatusJSON(out, e)
		},
	})
}

func (cmd *ApplyCmd) resolveDockerPath() string {
	if cmd.DockerPath != "" {
		return cmd.DockerPath
	}
	return pkgworkspace.DefaultDockerCommand
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
	if containerDetails.State.Status != devcconfig.ContainerStatusRunning {
		return nil, fmt.Errorf(
			"container %s is not running (status: %s)",
			cmd.Container,
			containerDetails.State.Status,
		)
	}
	return containerDetails, nil
}

func (cmd *ApplyCmd) loadConfig(
	ctx context.Context,
	containerDetails *devcconfig.ContainerDetails,
) (*devcconfig.Result, error) {
	var devContainerConfig *devcconfig.DevContainerConfig
	var err error

	if cmd.Config != "" {
		devContainerConfig, err = devcconfig.ParseDevContainerJSONFile(ctx, cmd.Config)
	} else {
		cwd, cwdErr := os.Getwd()
		if cwdErr != nil {
			return nil, fmt.Errorf("get working directory: %w", cwdErr)
		}
		devContainerConfig, err = devcconfig.ParseDevContainerJSON(ctx, cwd, "")
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
	emitJSON bool,
) error {
	if len(result.MergedConfig.Features) == 0 {
		return nil
	}

	featureSets, err := cmd.resolveFeatureSets(ctx, result)
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

	if err := cmd.copyAndExecFeatures(
		ctx,
		helper,
		featureStage{featureSets: featureSets, dir: featureStageDir},
		emitJSON,
	); err != nil {
		return err
	}

	return nil
}

func (cmd *ApplyCmd) resolveFeatureSets(
	ctx context.Context,
	result *devcconfig.Result,
) ([]*devcconfig.FeatureSet, error) {
	devContainerConfig := &devcconfig.DevContainerConfig{}
	devContainerConfig.Features = result.MergedConfig.Features
	devContainerConfig.OverrideFeatureInstallOrder = result.MergedConfig.OverrideFeatureInstallOrder
	devContainerConfig.Origin = result.MergedConfig.Origin

	featureSets, err := feature.ResolveFeatureOrderWithContext(ctx, devContainerConfig)
	if err != nil {
		return nil, fmt.Errorf("resolve features: %w", err)
	}
	return featureSets, nil
}

// featureStage is the feature set to copy into the container and the local
// folder it is staged in.
type featureStage struct {
	featureSets []*devcconfig.FeatureSet
	dir         string
}

func (cmd *ApplyCmd) copyAndExecFeatures(
	ctx context.Context,
	helper *docker.DockerHelper,
	stage featureStage,
	emitJSON bool,
) error {
	featureSets, featureStageDir := stage.featureSets, stage.dir
	containerFeaturesPath := "/tmp/build-features"
	streams := docker.Streams{Stdout: os.Stdout, Stderr: os.Stderr}
	if emitJSON {
		// Raw Docker output must never share stdout with status and result
		// envelopes. The configured logger preserves the selected machine
		// encoding on stderr instead.
		outputWriter := log.Writer(log.LevelInfo)
		defer func() { _ = outputWriter.Close() }()
		streams.Stdout = outputWriter
		streams.Stderr = outputWriter
	}

	cpArgs := []string{"cp", featureStageDir + "/.", cmd.Container + ":" + containerFeaturesPath}
	if err := helper.Run(
		ctx,
		cpArgs,
		streams,
	); err != nil {
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
		streams.Stdin = os.Stdin
		if err := helper.Run(ctx, execArgs, streams); err != nil {
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
		for i, h := range hook.cmds {
			indexed := *params
			indexed.HookIndex = i
			if err := workspace.ExecLifecycleHook(&indexed, hook.name, h); err != nil {
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
