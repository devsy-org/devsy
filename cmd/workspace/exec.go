package workspace

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/docker"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/output"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type ExecCmd struct {
	*flags.GlobalFlags

	WorkspaceName       string
	WorkspaceFolder     string
	ContainerID         string
	DockerPath          string
	RemoteEnv           []string
	DefaultUserEnvProbe string
	IDLabels            []string
	ContainerDataFolder string
	SkipPostCreate      bool
}

func NewExecCmd(f *flags.GlobalFlags) *cobra.Command {
	cmd := &ExecCmd{GlobalFlags: f}
	execCmd := &cobra.Command{
		Use:   "exec [workspace-name] [flags] -- <cmd> [args...]",
		Short: "Executes a command in a running workspace container",
		Args:  cobra.MinimumNArgs(1),
		ValidArgsFunction: func(
			cobraCmd *cobra.Command, args []string, toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return completion.GetWorkspaceSuggestions(
				cobraCmd.Root(), cmd.Context, cmd.Provider, args, toComplete, cmd.Owner,
			)
		},
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			ctx := cobraCmd.Context()

			dash := cobraCmd.ArgsLenAtDash()
			var nameArgs, cmdArgs []string
			if dash >= 0 {
				nameArgs = args[:dash]
				cmdArgs = args[dash:]
			} else {
				cmdArgs = args
			}

			if len(nameArgs) > 1 {
				return fmt.Errorf("expected at most one workspace name, got %d", len(nameArgs))
			}
			if len(nameArgs) == 1 {
				cmd.WorkspaceName = nameArgs[0]
			}
			if len(cmdArgs) == 0 {
				return fmt.Errorf("a command to execute is required after --")
			}

			return cmd.Run(ctx, cmdArgs)
		},
	}

	execCmd.Flags().
		StringVar(
			&cmd.WorkspaceFolder,
			"workspace-folder",
			"",
			"Path to the workspace folder",
		)
	execCmd.Flags().
		StringVar(
			&cmd.ContainerID,
			"container-id",
			"",
			"Target a specific container by ID",
		)
	execCmd.Flags().
		StringVar(
			&cmd.DockerPath,
			"docker-path",
			"",
			"Path to the docker/podman executable (defaults to 'docker')",
		)
	execCmd.Flags().
		StringSliceVar(
			&cmd.RemoteEnv,
			"remote-env",
			[]string{},
			"Environment variables to set in the container (KEY=VALUE format)",
		)
	execCmd.Flags().
		StringVar(
			&cmd.DefaultUserEnvProbe,
			"default-user-env-probe",
			"",
			"Override userEnvProbe from config (loginInteractiveShell, loginShell, interactiveShell, none)",
		)
	execCmd.Flags().
		StringArrayVar(
			&cmd.IDLabels,
			"id-label",
			[]string{},
			"Override the default container identification labels (format: key=value, can be specified multiple times)",
		)
	execCmd.Flags().
		StringVar(
			&cmd.ContainerDataFolder,
			"container-data-folder",
			"",
			"Override the default container data folder path",
		)
	execCmd.Flags().
		BoolVar(
			&cmd.SkipPostCreate,
			"skip-post-create",
			false,
			"Skip running postCreateCommand",
		)

	return execCmd
}

func (cmd *ExecCmd) Run(ctx context.Context, args []string) error {
	if cmd.ContainerDataFolder != "" {
		log.Warnf("--container-data-folder is accepted but not yet implemented for exec")
	}
	if cmd.SkipPostCreate {
		log.Warnf("--skip-post-create is accepted but not yet implemented for exec")
	}

	if err := cmd.validateRemoteEnv(); err != nil {
		return err
	}
	if err := devcconfig.ValidateIDLabels(cmd.IDLabels); err != nil {
		return err
	}

	if _, err := output.ResolveMode(cmd.ResultFormat); err != nil {
		return err
	}

	// Guard here (before the container-id branch) so name+container-id is rejected
	// rather than silently taking the container path. resolveExecTarget repeats this
	// check so it stays correct as a standalone unit.
	if cmd.WorkspaceName != "" && (cmd.WorkspaceFolder != "" || cmd.ContainerID != "") {
		return errFolderNameConflict
	}

	if cmd.ContainerID != "" {
		return cmd.runWithContainerID(ctx, args)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine current directory: %w", err)
	}
	getArgs, err := resolveExecTarget(cmd, cwd)
	if err != nil {
		return err
	}

	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	client, err := workspace2.Get(ctx, workspace2.GetOptions{
		DevsyConfig: devsyConfig,
		Args:        getArgs,
		Owner:       cmd.Owner,
	})
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}

	workspaceConfig := client.WorkspaceConfig()
	runtime := workspace2.NewDockerRuntime(workspaceConfig, cmd.DockerPath)

	containerDetails, err := runtime.FindRunning(
		ctx, devcontainer.GetRunnerIDFromWorkspace(workspaceConfig), cmd.IDLabels,
	)
	if err != nil {
		return err
	}

	result := workspace2.LoadExecResult(workspaceConfig, containerDetails)
	workdir := workspace2.ResolveExecWorkdir(result, client.Workspace())
	user := devcconfig.GetRemoteUser(result)
	userEnvProbe := resolveUserEnvProbe(result, cmd.DefaultUserEnvProbe)

	target := workspace2.ContainerTarget{
		ContainerID: containerDetails.ID,
		User:        user,
	}
	probedEnv := runtime.ProbeEnv(ctx, target, userEnvProbe)
	envMap := workspace2.BuildExecEnv(result, cmd.RemoteEnv, probedEnv)

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	emitJSON := mode == output.ModeJSON

	err = cmd.execInContainer(ctx, execOpts{
		dockerCmd: runtime.DockerCommand(),
		target:    target,
		workdir:   workdir,
		envMap:    envMap,
	}, args)
	if err != nil {
		if emitJSON {
			_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
		}
		return err
	}

	if emitJSON {
		_ = devcconfig.WriteResultJSON(os.Stderr, devcconfig.ResultEnvelope{
			ContainerID:           containerDetails.ID,
			RemoteUser:            user,
			RemoteWorkspaceFolder: workdir,
		})
	}
	return nil
}

func (cmd *ExecCmd) runWithContainerID(ctx context.Context, args []string) error {
	runtime := workspace2.NewDockerRuntime(nil, cmd.DockerPath)
	helper := &docker.DockerHelper{DockerCommand: runtime.DockerCommand()}

	details, err := helper.InspectContainers(ctx, []string{cmd.ContainerID})
	if err != nil {
		return fmt.Errorf("inspect container %s: %w", cmd.ContainerID, err)
	}
	if len(details) == 0 {
		return fmt.Errorf("container %s not found", cmd.ContainerID)
	}

	containerDetails := &details[0]
	if !strings.EqualFold(containerDetails.State.Status, workspace2.ContainerStatusRunning) {
		return fmt.Errorf(
			"container %s is not running (status: %s)",
			cmd.ContainerID,
			containerDetails.State.Status,
		)
	}

	userEnvProbe := cmd.DefaultUserEnvProbe
	target := workspace2.ContainerTarget{
		ContainerID: containerDetails.ID,
		User:        "",
	}
	probedEnv := runtime.ProbeEnv(ctx, target, userEnvProbe)
	envMap := workspace2.BuildExecEnv(nil, cmd.RemoteEnv, probedEnv)

	workdir := containerDetails.Config.WorkingDir

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	emitJSON := mode == output.ModeJSON

	err = cmd.execInContainer(ctx, execOpts{
		dockerCmd: runtime.DockerCommand(),
		target:    target,
		workdir:   workdir,
		envMap:    envMap,
	}, args)
	if err != nil {
		if emitJSON {
			_ = devcconfig.WriteErrorJSON(os.Stderr, err.Error())
		}
		return err
	}

	if emitJSON {
		_ = devcconfig.WriteResultJSON(os.Stderr, devcconfig.ResultEnvelope{
			ContainerID:           containerDetails.ID,
			RemoteWorkspaceFolder: workdir,
		})
	}
	return nil
}

var errFolderNameConflict = fmt.Errorf(
	"specify either a workspace name or --workspace-folder/--container-id, not both",
)

// resolveExecTarget decides what string is handed to workspace.Get.
// Precedence: explicit name, then --workspace-folder, then the current dir.
// A workspace name combined with --workspace-folder or --container-id is a conflict.
func resolveExecTarget(cmd *ExecCmd, cwd string) ([]string, error) {
	if cmd.WorkspaceName != "" && (cmd.WorkspaceFolder != "" || cmd.ContainerID != "") {
		return nil, errFolderNameConflict
	}
	switch {
	case cmd.WorkspaceName != "":
		return []string{cmd.WorkspaceName}, nil
	case cmd.WorkspaceFolder != "":
		return []string{cmd.WorkspaceFolder}, nil
	default:
		return []string{cwd}, nil
	}
}

func (cmd *ExecCmd) validateRemoteEnv() error {
	for _, env := range cmd.RemoteEnv {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return fmt.Errorf("invalid remote-env value %q: must be KEY=VALUE format", env)
		}
	}
	return nil
}

func resolveUserEnvProbe(result *devcconfig.Result, cliOverride string) string {
	if cliOverride != "" {
		return cliOverride
	}
	if result != nil && result.MergedConfig != nil {
		return result.MergedConfig.UserEnvProbe
	}
	return ""
}

type execOpts struct {
	dockerCmd string
	target    workspace2.ContainerTarget
	workdir   string
	envMap    map[string]string
}

func (cmd *ExecCmd) execInContainer(ctx context.Context, opts execOpts, args []string) error {
	execArgs := []string{"exec", "-i"}
	if term.IsTerminal(int(os.Stdin.Fd())) { // #nosec G115 -- fd is always a valid file descriptor
		execArgs = append(execArgs, "-t")
	}
	for k, v := range opts.envMap {
		execArgs = append(execArgs, "-e", k+"="+v)
	}
	if opts.workdir != "" {
		execArgs = append(execArgs, "--workdir", opts.workdir)
	}
	if opts.target.User != "" {
		execArgs = append(execArgs, "--user", opts.target.User)
	}
	execArgs = append(execArgs, opts.target.ContainerID)
	execArgs = append(execArgs, args...)

	redacted := strings.Join(redactExecArgs(execArgs), " ")
	log.Debugf("Executing in container: %s %s", opts.dockerCmd, redacted)

	helper := &docker.DockerHelper{DockerCommand: opts.dockerCmd}
	return helper.Run(ctx, execArgs, os.Stdin, os.Stdout, os.Stderr)
}

func redactExecArgs(args []string) []string {
	redacted := make([]string, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "-e" && i+1 < len(args) {
			redacted[i] = args[i]
			i++
			if k, _, ok := strings.Cut(args[i], "="); ok {
				redacted[i] = k + "=<redacted>"
			} else {
				redacted[i] = args[i]
			}
		} else {
			redacted[i] = args[i]
		}
	}
	return redacted
}
