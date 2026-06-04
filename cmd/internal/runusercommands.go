package cmdinternal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/workspace"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/types"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// RunUserCommandsCmd holds the run-user-commands command flags.
type RunUserCommandsCmd struct {
	*flags.GlobalFlags

	WorkspaceFolder         string
	ContainerID             string
	DockerPath              string
	Config                  string
	OverrideConfig          string
	RemoteEnv               []string
	IDLabels                []string
	Prebuild                bool
	SkipNonBlockingCommands bool
	SkipPostCreate          bool
	SkipPostStart           bool
	SkipPostAttach          bool
	SkipOnCreate            bool
	SkipUpdateContent       bool
}

// NewRunUserCommandsCmd creates a new run-user-commands command.
func NewRunUserCommandsCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &RunUserCommandsCmd{GlobalFlags: f}
	runE := func(cobraCmd *cobra.Command, _ []string) error {
		return cmd.Run(cobraCmd.Context())
	}

	runCmd := &cobra.Command{
		Use:   "run-user-commands",
		Short: "Executes lifecycle commands in a running workspace container",
		RunE:  runE,
	}

	runCmd.Flags().
		StringVar(
			&cmd.WorkspaceFolder,
			"workspace-folder",
			"",
			"Path to the workspace folder",
		)
	runCmd.Flags().
		StringVar(
			&cmd.ContainerID,
			"container-id",
			"",
			"Target a specific container by ID",
		)
	runCmd.Flags().
		StringVar(
			&cmd.DockerPath,
			"docker-path",
			"",
			"Path to the docker/podman executable (defaults to 'docker')",
		)
	runCmd.Flags().
		StringVar(
			&cmd.Config,
			"config",
			"",
			"Path to the devcontainer.json configuration file",
		)
	runCmd.Flags().
		StringVar(
			&cmd.OverrideConfig,
			"override-config",
			"",
			"Path to an additional devcontainer.json file to override the primary configuration",
		)
	runCmd.Flags().
		StringArrayVar(
			&cmd.RemoteEnv,
			"remote-env",
			[]string{},
			"Environment variables to set in the container (KEY=VALUE format, can be specified multiple times)",
		)
	runCmd.Flags().
		StringArrayVar(
			&cmd.IDLabels,
			"id-label",
			[]string{},
			"Override the default container identification labels (format: key=value, can be specified multiple times)",
		)
	runCmd.Flags().
		BoolVar(&cmd.Prebuild, "prebuild", false,
			"Stop lifecycle execution after onCreateCommand and updateContentCommand")
	runCmd.Flags().
		BoolVar(&cmd.SkipNonBlockingCommands, "skip-non-blocking-commands", false,
			"Skip non-blocking lifecycle commands (stop after the waitFor-configured command)")
	runCmd.Flags().
		BoolVar(&cmd.SkipPostCreate, "skip-post-create", false, "Skip running postCreateCommand")
	runCmd.Flags().
		BoolVar(&cmd.SkipPostStart, "skip-post-start", false, "Skip running postStartCommand")
	runCmd.Flags().
		BoolVar(&cmd.SkipPostAttach, "skip-post-attach", false, "Skip running postAttachCommand")
	runCmd.Flags().
		BoolVar(&cmd.SkipOnCreate, "skip-on-create", false, "Skip running onCreateCommand")
	runCmd.Flags().
		BoolVar(&cmd.SkipUpdateContent, "skip-update-content", false, "Skip running updateContentCommand")

	return runCmd
}

// NewRunUserCommandsCmdAlias creates the hidden camelCase alias for devcontainer CLI compat.
func NewRunUserCommandsCmdAlias(f *flags.GlobalFlags) *cobra.Command {
	primary := NewRunUserCommandsCmd(f)
	primary.Use = "runUserCommands"
	primary.Hidden = true
	return primary
}

const updateContentCommand = "updateContentCommand"

// Run executes the run-user-commands logic.
func (cmd *RunUserCommandsCmd) Run(ctx context.Context) error {
	if err := cmd.validate(); err != nil {
		return err
	}

	if cmd.ContainerID != "" {
		return cmd.runWithContainerID(ctx)
	}

	params, result, err := cmd.resolveContainer(ctx)
	if err != nil {
		return err
	}

	if err := cmd.runLifecycleHooks(params, result); err != nil {
		return err
	}

	user := devcconfig.GetRemoteUser(result)
	log.Infof("lifecycle commands completed for container %s", params.ContainerID)
	_ = devcconfig.WriteResultJSON(os.Stderr, devcconfig.ResultEnvelope{
		ContainerID:           params.ContainerID,
		RemoteUser:            user,
		RemoteWorkspaceFolder: params.Workdir,
	})
	return nil
}

func (cmd *RunUserCommandsCmd) validate() error {
	if cmd.WorkspaceFolder == "" && cmd.ContainerID == "" {
		return fmt.Errorf("either --workspace-folder or --container-id must be provided")
	}
	if cmd.ContainerID != "" && cmd.WorkspaceFolder == "" && cmd.Config == "" {
		return fmt.Errorf(
			"--config is required when --container-id is used without --workspace-folder",
		)
	}
	if err := cmd.validateRemoteEnv(); err != nil {
		return err
	}
	return devcconfig.ValidateIDLabels(cmd.IDLabels)
}

func (cmd *RunUserCommandsCmd) validateRemoteEnv() error {
	for _, env := range cmd.RemoteEnv {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return fmt.Errorf("invalid remote-env value %q: must be KEY=VALUE format", env)
		}
	}
	return nil
}

func (cmd *RunUserCommandsCmd) runWithContainerID(ctx context.Context) error {
	helper := &docker.DockerHelper{DockerCommand: cmd.resolveDockerPath()}

	containerDetails, err := cmd.inspectRunningContainer(ctx, helper)
	if err != nil {
		return err
	}

	result, err := cmd.loadContainerIDConfig(containerDetails)
	if err != nil {
		return err
	}

	workdir := containerDetails.Config.WorkingDir
	if result.MergedConfig.WorkspaceFolder != "" {
		workdir = result.MergedConfig.WorkspaceFolder
	}

	envArgs := workspace.BuildLifecycleEnvArgs(result)
	envArgs = append(envArgs, cmd.buildCLIRemoteEnvArgs()...)

	params := &workspace.LifecycleExecParams{
		Ctx:         ctx,
		Helper:      helper,
		ContainerID: containerDetails.ID,
		EnvArgs:     envArgs,
		Workdir:     workdir,
		User:        devcconfig.GetRemoteUser(result),
	}

	if err := cmd.runLifecycleHooks(params, result); err != nil {
		return err
	}

	user := devcconfig.GetRemoteUser(result)
	log.Infof("lifecycle commands completed for container %s", params.ContainerID)
	_ = devcconfig.WriteResultJSON(os.Stderr, devcconfig.ResultEnvelope{
		ContainerID:           params.ContainerID,
		RemoteUser:            user,
		RemoteWorkspaceFolder: params.Workdir,
	})
	return nil
}

func (cmd *RunUserCommandsCmd) resolveDockerPath() string {
	if cmd.DockerPath != "" {
		return cmd.DockerPath
	}
	return workspace2.DefaultDockerCommand
}

func (cmd *RunUserCommandsCmd) inspectRunningContainer(
	ctx context.Context,
	helper *docker.DockerHelper,
) (*devcconfig.ContainerDetails, error) {
	details, err := helper.InspectContainers(ctx, []string{cmd.ContainerID})
	if err != nil {
		_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
		return nil, fmt.Errorf("inspect container %s: %w", cmd.ContainerID, err)
	}
	if len(details) == 0 {
		errMsg := fmt.Sprintf("container %s not found", cmd.ContainerID)
		_ = devcconfig.WriteErrorJSON(os.Stderr, errMsg)
		return nil, errors.New(errMsg)
	}

	containerDetails := &details[0]
	if !strings.EqualFold(containerDetails.State.Status, workspace2.ContainerStatusRunning) {
		errMsg := fmt.Sprintf(
			"container %s is not running (status: %s)",
			cmd.ContainerID,
			containerDetails.State.Status,
		)
		_ = devcconfig.WriteErrorJSON(os.Stderr, errMsg)
		return nil, errors.New(errMsg)
	}
	return containerDetails, nil
}

func (cmd *RunUserCommandsCmd) loadContainerIDConfig(
	containerDetails *devcconfig.ContainerDetails,
) (*devcconfig.Result, error) {
	configFolder := cmd.WorkspaceFolder
	if configFolder == "" {
		configFolder = "."
	}

	devContainerConfig, err := devcconfig.ParseDevContainerJSON(configFolder, cmd.Config)
	if err != nil {
		_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
		return nil, fmt.Errorf("parse devcontainer config: %w", err)
	}
	if devContainerConfig == nil {
		errMsg := "no devcontainer configuration found"
		_ = devcconfig.WriteErrorJSON(os.Stderr, errMsg)
		return nil, errors.New(errMsg)
	}

	mergedConfig, err := devcconfig.MergeConfiguration(devContainerConfig, nil)
	if err != nil {
		_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
		return nil, fmt.Errorf("merge configuration: %w", err)
	}

	if cmd.OverrideConfig != "" {
		if err := devcconfig.MergeExtraRemoteEnv(mergedConfig, cmd.OverrideConfig); err != nil {
			_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
			return nil, fmt.Errorf("apply override config: %w", err)
		}
	}

	return &devcconfig.Result{
		MergedConfig:     mergedConfig,
		ContainerDetails: containerDetails,
	}, nil
}

func (cmd *RunUserCommandsCmd) buildCLIRemoteEnvArgs() []string {
	if len(cmd.RemoteEnv) == 0 {
		return nil
	}
	args := make([]string, 0, len(cmd.RemoteEnv)*2)
	for _, env := range cmd.RemoteEnv {
		args = append(args, "-e", env)
	}
	return args
}

func (cmd *RunUserCommandsCmd) resolveContainer(
	ctx context.Context,
) (*workspace.LifecycleExecParams, *devcconfig.Result, error) {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return nil, nil, err
	}

	client, err := workspace2.Get(ctx, workspace2.GetOptions{
		DevsyConfig: devsyConfig,
		Args:        []string{cmd.WorkspaceFolder},
		Owner:       cmd.Owner,
	})
	if err != nil {
		_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
		return nil, nil, fmt.Errorf("resolve workspace: %w", err)
	}

	workspaceConfig := client.WorkspaceConfig()
	dockerCommand := workspace2.ResolveDockerCommand(workspaceConfig, cmd.DockerPath)

	containerDetails, err := workspace2.FindRunningContainer(
		ctx, dockerCommand, devcontainer.GetRunnerIDFromWorkspace(workspaceConfig), cmd.IDLabels,
	)
	if err != nil {
		_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
		return nil, nil, err
	}

	result := workspace2.LoadExecResult(workspaceConfig, containerDetails)
	if result == nil || result.MergedConfig == nil {
		_ = devcconfig.WriteErrorJSON(
			os.Stderr,
			"no workspace result found; lifecycle commands unavailable",
		)
		return nil, nil, fmt.Errorf("no workspace result found; lifecycle commands unavailable")
	}

	if cmd.OverrideConfig != "" {
		if err := devcconfig.MergeExtraRemoteEnv(
			result.MergedConfig,
			cmd.OverrideConfig,
		); err != nil {
			_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
			return nil, nil, fmt.Errorf("apply override config: %w", err)
		}
	}

	envArgs := workspace.BuildLifecycleEnvArgs(result)
	envArgs = append(envArgs, cmd.buildCLIRemoteEnvArgs()...)

	params := &workspace.LifecycleExecParams{
		Ctx:         ctx,
		Helper:      &docker.DockerHelper{DockerCommand: dockerCommand},
		ContainerID: containerDetails.ID,
		EnvArgs:     envArgs,
		Workdir:     workspace2.ResolveExecWorkdir(result, client.Workspace()),
		User:        devcconfig.GetRemoteUser(result),
	}
	return params, result, nil
}

func (cmd *RunUserCommandsCmd) runLifecycleHooks(
	params *workspace.LifecycleExecParams,
	result *devcconfig.Result,
) error {
	hooks := []struct {
		name string
		cmds []types.LifecycleHook
		skip bool
	}{
		{"onCreateCommand", result.MergedConfig.OnCreateCommands, cmd.SkipOnCreate},
		{updateContentCommand, result.MergedConfig.UpdateContentCommands, cmd.SkipUpdateContent},
		{"postCreateCommand", result.MergedConfig.PostCreateCommands, cmd.SkipPostCreate},
		{"postStartCommand", result.MergedConfig.PostStartCommands, cmd.SkipPostStart},
		{"postAttachCommand", result.MergedConfig.PostAttachCommands, cmd.SkipPostAttach},
	}

	waitForBoundary := resolveWaitForBoundary(result)

	for i, hook := range hooks {
		if cmd.Prebuild && i >= 2 {
			log.Infof("stopping lifecycle execution (--prebuild: after %s)", updateContentCommand)
			return nil
		}
		if cmd.SkipNonBlockingCommands && i > waitForBoundary {
			log.Infof(
				"stopping lifecycle execution (--skip-non-blocking-commands: after %s)",
				hooks[waitForBoundary].name,
			)
			return nil
		}
		if hook.skip {
			log.Infof("skipping %s (--skip flag set)", hook.name)
			continue
		}
		for _, h := range hook.cmds {
			if err := workspace.ExecLifecycleHook(params, hook.name, h); err != nil {
				_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
				return fmt.Errorf("lifecycle hooks: %s: %w", hook.name, err)
			}
		}
	}
	return nil
}

func resolveWaitForBoundary(result *devcconfig.Result) int {
	if result == nil || result.MergedConfig == nil {
		return 1
	}
	hookNames := []string{
		"onCreateCommand",
		updateContentCommand,
		"postCreateCommand",
		"postStartCommand",
		"postAttachCommand",
	}
	waitFor := result.MergedConfig.WaitFor
	if waitFor == "" {
		waitFor = updateContentCommand
	}
	for i, name := range hookNames {
		if name == waitFor {
			return i
		}
	}
	return 1
}
