package cmd

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/devsy-org/devsy/cmd/flags"
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

	WorkspaceFolder   string
	IDLabels          []string
	SkipPostCreate    bool
	SkipPostStart     bool
	SkipPostAttach    bool
	SkipOnCreate      bool
	SkipUpdateContent bool
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
	_ = runCmd.MarkFlagRequired("workspace-folder")
	runCmd.Flags().
		StringArrayVar(
			&cmd.IDLabels,
			"id-label",
			[]string{},
			"Override the default container identification labels (format: key=value, can be specified multiple times)",
		)
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

type lifecycleExecParams struct {
	ctx         context.Context
	helper      *docker.DockerHelper
	containerID string
	envArgs     []string
	workdir     string
}

// Run executes the run-user-commands logic.
func (cmd *RunUserCommandsCmd) Run(ctx context.Context) error {
	if err := devcconfig.ValidateIDLabels(cmd.IDLabels); err != nil {
		return err
	}

	params, result, err := cmd.resolveContainer(ctx)
	if err != nil {
		return err
	}

	if err := cmd.runLifecycleHooks(params, result); err != nil {
		return err
	}

	user := devcconfig.GetRemoteUser(result)
	log.Infof("lifecycle commands completed for container %s", params.containerID)
	_ = devcconfig.WriteResultJSON(os.Stderr, params.containerID, user, params.workdir, nil)
	return nil
}

func (cmd *RunUserCommandsCmd) resolveContainer(
	ctx context.Context,
) (*lifecycleExecParams, *devcconfig.Result, error) {
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
	dockerCommand := resolveDockerCommand(workspaceConfig)

	containerDetails, err := findRunningContainer(
		ctx, dockerCommand, devcontainer.GetRunnerIDFromWorkspace(workspaceConfig), cmd.IDLabels,
	)
	if err != nil {
		_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
		return nil, nil, err
	}

	result := loadExecResult(workspaceConfig, containerDetails)
	if result == nil || result.MergedConfig == nil {
		_ = devcconfig.WriteErrorJSON(
			os.Stderr,
			"no workspace result found; lifecycle commands unavailable",
		)
		return nil, nil, fmt.Errorf("no workspace result found; lifecycle commands unavailable")
	}

	params := &lifecycleExecParams{
		ctx:         ctx,
		helper:      &docker.DockerHelper{DockerCommand: dockerCommand},
		containerID: containerDetails.ID,
		envArgs:     buildLifecycleEnvArgs(result),
		workdir:     resolveExecWorkdir(result, client.Workspace()),
	}
	return params, result, nil
}

func (cmd *RunUserCommandsCmd) runLifecycleHooks(
	params *lifecycleExecParams,
	result *devcconfig.Result,
) error {
	hooks := []struct {
		name string
		cmds []types.LifecycleHook
		skip bool
	}{
		{"onCreateCommand", result.MergedConfig.OnCreateCommands, cmd.SkipOnCreate},
		{"updateContentCommand", result.MergedConfig.UpdateContentCommands, cmd.SkipUpdateContent},
		{"postCreateCommand", result.MergedConfig.PostCreateCommands, cmd.SkipPostCreate},
		{"postStartCommand", result.MergedConfig.PostStartCommands, cmd.SkipPostStart},
		{"postAttachCommand", result.MergedConfig.PostAttachCommands, cmd.SkipPostAttach},
	}

	for _, hook := range hooks {
		if hook.skip {
			log.Infof("skipping %s (--skip flag set)", hook.name)
			continue
		}
		for _, h := range hook.cmds {
			if err := execLifecycleHook(params, hook.name, h); err != nil {
				_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
				return fmt.Errorf("lifecycle hooks: %s: %w", hook.name, err)
			}
		}
	}
	return nil
}

func execLifecycleHook(params *lifecycleExecParams, name string, hook types.LifecycleHook) error {
	if len(hook) == 0 {
		return nil
	}

	for key, command := range hook {
		if len(command) == 0 {
			continue
		}
		log.Infof("running %s: %s %v", name, key, command)

		args := buildDockerExecArgs(params.containerID, params.envArgs, params.workdir, command)
		if err := params.helper.Run(params.ctx, args, os.Stdin, os.Stdout, os.Stderr); err != nil {
			return fmt.Errorf("command %q failed: %w", key, err)
		}
	}

	return nil
}

func buildLifecycleEnvArgs(result *devcconfig.Result) []string {
	if result == nil || result.MergedConfig == nil {
		return nil
	}

	env := result.MergedConfig.RemoteEnv
	if len(env) == 0 {
		return nil
	}

	keys := make([]string, 0, len(env))
	for k, v := range env {
		if v != nil {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	args := make([]string, 0, len(keys)*2)
	for _, k := range keys {
		args = append(args, "-e", k+"="+*env[k])
	}
	return args
}
