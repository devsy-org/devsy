package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/types"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// RunUserCommandsCmd holds the run-user-commands command flags.
type RunUserCommandsCmd struct {
	*flags.GlobalFlags

	WorkspaceFolder string
	IDLabels        []string
}

// NewRunUserCommandsCmd creates a new run-user-commands command.
func NewRunUserCommandsCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &RunUserCommandsCmd{GlobalFlags: f}
	runCmd := &cobra.Command{
		Use:     "run-user-commands",
		Aliases: []string{"runUserCommands"},
		Short:   "Executes lifecycle commands in a running workspace container",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
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

	return runCmd
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
	workdir := params.workdir
	log.Infof("lifecycle commands completed for container %s", params.containerID)
	_ = devcconfig.WriteResultJSON(os.Stdout, params.containerID, user, workdir)
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
		_ = devcconfig.WriteErrorJSON(os.Stdout, err.Error())
		return nil, nil, fmt.Errorf("resolve workspace: %w", err)
	}

	workspaceConfig := client.WorkspaceConfig()
	dockerCommand := resolveDockerCommand(workspaceConfig)

	containerDetails, err := findRunningContainer(
		ctx, dockerCommand, devcontainerGetRunnerID(workspaceConfig), cmd.IDLabels,
	)
	if err != nil {
		_ = devcconfig.WriteErrorJSON(os.Stdout, err.Error())
		return nil, nil, err
	}

	result := loadExecResult(workspaceConfig, containerDetails)
	if result == nil || result.MergedConfig == nil {
		_ = devcconfig.WriteErrorJSON(
			os.Stdout,
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
	}{
		{"postCreateCommand", result.MergedConfig.PostCreateCommands},
		{"postStartCommand", result.MergedConfig.PostStartCommands},
		{"postAttachCommand", result.MergedConfig.PostAttachCommands},
	}

	for _, hook := range hooks {
		for _, h := range hook.cmds {
			if err := execLifecycleHook(params, hook.name, h); err != nil {
				_ = devcconfig.WriteErrorJSON(os.Stdout, err.Error())
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

	args := make([]string, 0, len(env)*2)
	for k, v := range env {
		if v != nil {
			args = append(args, "-e", k+"="+*v)
		}
	}
	return args
}

func devcontainerGetRunnerID(ws *provider2.Workspace) string {
	return devcontainer.GetRunnerIDFromWorkspace(ws)
}
