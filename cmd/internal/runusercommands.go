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
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
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

	cliflags.Add(
		runCmd,
		cliflags.String(
			&cmd.WorkspaceFolder,
			names.WorkspaceFolder,
			"",
			"Path to the workspace folder",
		),
		cliflags.String(
			&cmd.ContainerID,
			names.ContainerID,
			"",
			"Target a specific container by ID",
		),
		cliflags.String(
			&cmd.DockerPath,
			names.DockerPath,
			"",
			"Path to the docker/podman executable (defaults to 'docker')",
		),
		cliflags.String(
			&cmd.Config,
			names.Config,
			"",
			"Path to the devcontainer.json configuration file",
		),
		cliflags.String(
			&cmd.OverrideConfig,
			names.OverrideConfig,
			"",
			"Path to an additional devcontainer.json file to override the primary configuration",
		),
		cliflags.StringArray(
			&cmd.RemoteEnv,
			names.RemoteEnv,
			[]string{},
			"Environment variables to set in the container (KEY=VALUE format, can be specified multiple times)",
		),
		cliflags.StringArray(
			&cmd.IDLabels,
			names.IDLabel,
			[]string{},
			"Override the default container identification labels (format: key=value, can be specified multiple times)",
		),
		cliflags.Bool(
			&cmd.Prebuild,
			names.Prebuild,
			false,
			"Stop lifecycle execution after onCreateCommand and updateContentCommand",
		),
		cliflags.Bool(
			&cmd.SkipNonBlockingCommands,
			names.SkipNonBlockingCommands,
			false,
			"Skip non-blocking lifecycle commands (stop after the waitFor-configured command)",
		),
		cliflags.Bool(
			&cmd.SkipPostCreate,
			names.SkipPostCreate,
			false,
			"Skip running postCreateCommand",
		),
		cliflags.Bool(
			&cmd.SkipPostStart,
			names.SkipPostStart,
			false,
			"Skip running postStartCommand",
		),
		cliflags.Bool(
			&cmd.SkipPostAttach,
			names.SkipPostAttach,
			false,
			"Skip running postAttachCommand",
		),
		cliflags.Bool(&cmd.SkipOnCreate, names.SkipOnCreate, false, "Skip running onCreateCommand"),
		cliflags.Bool(
			&cmd.SkipUpdateContent,
			names.SkipUpdateContent,
			false,
			"Skip running updateContentCommand",
		),
	)

	runCmd.MarkFlagsOneRequired(names.WorkspaceFolder, names.ContainerID)

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

	result, err := cmd.loadContainerIDConfig(ctx, containerDetails)
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
	ctx context.Context,
	containerDetails *devcconfig.ContainerDetails,
) (*devcconfig.Result, error) {
	configFolder := cmd.WorkspaceFolder
	if configFolder == "" {
		configFolder = "."
	}

	devContainerConfig, err := devcconfig.ParseDevContainerJSON(
		ctx,
		configFolder,
		cmd.Config,
	)
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
		if err := devcconfig.MergeExtraRemoteEnv(
			ctx,
			mergedConfig,
			cmd.OverrideConfig,
		); err != nil {
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
	runtime, err := workspace2.NewContainerRuntime(workspaceConfig, cmd.DockerPath)
	if err != nil {
		return nil, nil, err
	}

	containerDetails, err := runtime.FindRunning(
		ctx, devcontainer.GetRunnerIDFromWorkspace(workspaceConfig), cmd.IDLabels,
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
			ctx,
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
		Ctx: ctx,
		Helper: &docker.DockerHelper{
			DockerCommand: runtime.Command(),
			Environment:   runtime.Environment(),
		},
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
			log.Infof(
				"stopping lifecycle execution (%s: after %s)",
				names.Flag(names.Prebuild),
				updateContentCommand,
			)
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
