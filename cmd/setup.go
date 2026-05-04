package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/spf13/cobra"
)

const (
	flagSetUpContainer       = "container"
	flagSetUpConfig          = "config"
	flagSetUpWorkspaceFolder = "workspace-folder"
	defaultWorkspaceDir      = "/workspaces"
	dockerExecSubcommand     = "exec"
)

// SetUpCmd holds the set-up command flags.
type SetUpCmd struct {
	*flags.GlobalFlags

	Container       string
	Config          string
	WorkspaceFolder string
}

// NewSetUpCmd creates a new set-up command.
func NewSetUpCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &SetUpCmd{GlobalFlags: f}
	setupCmd := &cobra.Command{
		Use:   "set-up",
		Short: "Apply devcontainer configuration to a running container",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
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

	return setupCmd
}

// Run executes the set-up command logic.
func (cmd *SetUpCmd) Run(ctx context.Context) error {
	devContainerConfig, err := cmd.loadConfig()
	if err != nil {
		return fmt.Errorf("load devcontainer config: %w", err)
	}
	if devContainerConfig == nil {
		return fmt.Errorf("no devcontainer.json found")
	}

	workspaceFolder := cmd.resolveWorkspaceFolder()
	helper := &docker.DockerHelper{DockerCommand: defaultDockerCommand}
	envArgs := buildContainerEnvArgs(devContainerConfig.ContainerEnv)

	opts := hookExecOpts{
		ctx:             ctx,
		helper:          helper,
		envArgs:         envArgs,
		workspaceFolder: workspaceFolder,
	}

	if err := cmd.execHook(opts, devContainerConfig.PostCreateCommand); err != nil {
		return fmt.Errorf("lifecycle hooks: postCreateCommand: %w", err)
	}

	if err := cmd.execHook(opts, devContainerConfig.PostStartCommand); err != nil {
		return fmt.Errorf("lifecycle hooks: postStartCommand: %w", err)
	}

	log.Infof("set-up completed for container %s", cmd.Container)
	return nil
}

type hookExecOpts struct {
	ctx             context.Context
	helper          *docker.DockerHelper
	envArgs         []string
	workspaceFolder string
}

func (cmd *SetUpCmd) loadConfig() (*config.DevContainerConfig, error) {
	if cmd.Config != "" {
		return config.ParseDevContainerJSONFile(cmd.Config)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("get working directory: %w", err)
	}
	return config.ParseDevContainerJSON(cwd, "")
}

func (cmd *SetUpCmd) resolveWorkspaceFolder() string {
	if cmd.WorkspaceFolder != "" {
		return cmd.WorkspaceFolder
	}

	cwd, err := os.Getwd()
	if err != nil {
		return defaultWorkspaceDir
	}
	return filepath.Join(defaultWorkspaceDir, filepath.Base(cwd))
}

func (cmd *SetUpCmd) execHook(opts hookExecOpts, hook types.LifecycleHook) error {
	if len(hook) == 0 {
		return nil
	}

	for key, command := range hook {
		if len(command) == 0 {
			continue
		}
		log.Infof("executing lifecycle hook: %s %v", key, command)

		args := buildDockerExecArgs(cmd.Container, opts.envArgs, opts.workspaceFolder, command)
		if err := opts.helper.Run(opts.ctx, args, os.Stdin, os.Stdout, os.Stderr); err != nil {
			return fmt.Errorf("command %q failed: %w", key, err)
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
