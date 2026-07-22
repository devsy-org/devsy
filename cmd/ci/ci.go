package ci

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	workspacecmd "github.com/devsy-org/devsy/cmd/workspace"
	"github.com/devsy-org/devsy/cmd/workspace/up"
	"github.com/devsy-org/devsy/pkg/client"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// teardownTimeout bounds ephemeral-workspace deletion, which runs on its own
// context so it completes even after the command context is cancelled.
const teardownTimeout = 2 * time.Minute

// CICmd holds the ci command flags.
type CICmd struct {
	*flags.GlobalFlags
	provider.CLIOptions

	ProviderOptions []string

	// RunCmd is the argv executed in the container, resolved from either the
	// tokens after "--" or a --run-cmd shell string.
	RunCmd []string
	// RunCmdString is the raw --run-cmd shell command, run via "sh -c".
	RunCmdString       string
	RemoteEnv          []string
	Keep               bool
	Machine            string
	SecretsFile        string
	FeatureSecretsFile string
}

// NewCICmd creates a new ci command.
func NewCICmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &CICmd{GlobalFlags: flags}
	ciCmd := &cobra.Command{
		Use:   "ci [flags] [workspace-path|workspace-name] -- <cmd> [args...]",
		Short: "Build a devcontainer, run a command inside it, then tear it down",
		Long: `Builds an ephemeral devcontainer, runs the given command inside it, and
deletes the workspace afterwards. Useful for validating that a devcontainer
builds and behaves correctly in CI. The command runs on any CI system by
invoking the Devsy binary directly.

The command to run is given after "--" or via --run-cmd. A non-zero exit from
that command propagates as the exit code of "devsy ci".`,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			source, runCmd, err := splitArgs(cobraCmd, args, cmd.RunCmdString)
			if err != nil {
				return err
			}
			cmd.RunCmd = runCmd
			return cmd.execute(cobraCmd.Context(), source)
		},
	}

	cmd.registerFlags(ciCmd)
	return ciCmd
}

// runCmdFlag is ci-specific and has no shared name constant.
const runCmdFlag = "run-cmd"

// keepFlag is ci-specific and has no shared name constant.
const keepFlag = "keep"

func (cmd *CICmd) registerFlags(ciCmd *cobra.Command) {
	cliflags.Add(ciCmd,
		cliflags.String(&cmd.RunCmdString, runCmdFlag, "",
			"Shell command to run inside the container, executed via sh -c "+
				"(alternative to specifying an argv after --)"),
		cliflags.StringSlice(&cmd.RemoteEnv, names.RemoteEnv, nil,
			"Environment variables to set in the container at run time (KEY=VALUE, repeatable)"),
		cliflags.Bool(&cmd.Keep, keepFlag, false,
			"Keep the workspace after running instead of tearing it down (useful for debugging)"),
		cliflags.String(&cmd.DevContainerSource, names.DevContainer, "",
			"Select the devcontainer config source, overriding project discovery: "+
				`"none" (ignore the project config), "image:<ref>" (use only that image), `+
				`"id:<name>" (a named .devcontainer/<name> profile), or a path to a devcontainer.json`),
		cliflags.StringSlice(&cmd.ProviderOptions, names.ProviderOption, nil,
			"Provider option in the form KEY=VALUE"),
		cliflags.String(&cmd.Machine, names.Machine, "",
			"The machine to use for this workspace. The machine needs to exist beforehand"),
		cliflags.Bool(&cmd.NoCache, names.NoCache, false,
			"Do not use the build cache when building the image"),
		cliflags.String(&cmd.RunPlatform, names.Platform, "",
			"Run the container under a specific platform via emulation (e.g. linux/amd64)"),
		cliflags.Value(&cmd.GitCloneStrategy, names.GitCloneStrategy,
			"The git clone strategy Devsy uses to checkout git based workspaces "+
				"(full, blobless, treeless or shallow)"),
		cliflags.Bool(&cmd.GitCloneRecursiveSubmodules, names.GitRecurseSubmodules, false,
			"Clone submodules recursively"),
		cliflags.StringArray(&cmd.CacheFrom, names.CacheFrom, nil,
			"Cache sources for the build (e.g., myregistry.io/cache:latest). "+
				"Reuse a pre-built image to warm the build"),
		cliflags.StringArray(&cmd.WorkspaceEnv, names.WorkspaceEnv, nil,
			"Env variables available at build/lifecycle time (KEY=VALUE, repeatable)"),
		cliflags.StringSlice(&cmd.WorkspaceEnvFile, names.WorkspaceEnvFile, nil,
			"Path to files containing a list of extra env variables to put into the workspace"),
		cliflags.StringArray(
			&cmd.InitEnv,
			names.InitEnv,
			nil,
			"Extra env variables to inject during workspace initialization (KEY=VALUE, repeatable)",
		),
		cliflags.String(
			&cmd.SecretsFile,
			names.SecretsFile,
			"",
			"Path to a dotenv-style file containing KEY=VALUE secrets injected into lifecycle commands",
		),
		cliflags.String(&cmd.FeatureSecretsFile, names.FeatureSecretsFile, "",
			"Path to a JSON file containing secret values for features, format: "+
				`{"featureId": {"optionName": "value"}}`),
	)
	cliflags.RegisterDevContainerModifierFlags(ciCmd.Flags(), cliflags.DevContainerModifierFlags{
		Features: &cmd.AdditionalFeatures,
	})
}

// splitArgs separates the optional workspace source from the run command. The
// command is the explicit argv after "--", or, when that is absent, the
// --run-cmd shell string wrapped in "sh -c".
func splitArgs(
	cobraCmd *cobra.Command, args []string, runCmdString string,
) (string, []string, error) {
	dash := cobraCmd.ArgsLenAtDash()

	var sourceArgs, cmdArgs []string
	if dash >= 0 {
		sourceArgs = args[:dash]
		cmdArgs = args[dash:]
	} else {
		sourceArgs = args
	}

	if len(sourceArgs) > 1 {
		return "", nil, fmt.Errorf("expected at most one workspace source, got %d", len(sourceArgs))
	}

	if len(cmdArgs) == 0 {
		if runCmdString != "" {
			cmdArgs = []string{"sh", "-c", runCmdString}
		}
	} else if runCmdString != "" {
		return "", nil, fmt.Errorf("specify the command after -- or via --run-cmd, not both")
	}
	if len(cmdArgs) == 0 {
		return "", nil, fmt.Errorf("a command to run is required after -- or via --run-cmd")
	}

	source := ""
	if len(sourceArgs) == 1 {
		source = sourceArgs[0]
	}
	return source, cmdArgs, nil
}

func (cmd *CICmd) execute(ctx context.Context, source string) (err error) {
	if err := cmd.validateEnv(); err != nil {
		return err
	}
	if err := devcontainer.ResolveSourceSpec(&cmd.CLIOptions); err != nil {
		return err
	}

	// Convert SIGINT/SIGTERM (e.g. a cancelled CI job) into context
	// cancellation so the deferred teardown still runs instead of leaking the
	// workspace.
	ctx, cancel := up.WithSignals(ctx)
	defer cancel()

	devsyConfig, cfgErr := config.LoadConfig(cmd.Context, cmd.Provider)
	if cfgErr != nil {
		return cfgErr
	}
	if devsyConfig.ContextOption(config.ContextOptionSSHStrictHostKeyChecking) == config.BoolTrue {
		cmd.StrictHostKeyChecking = true
	}

	workspaceClient, cleanup, resolveErr := cmd.resolveWorkspace(ctx, devsyConfig, source)
	if resolveErr != nil {
		return resolveErr
	}
	// cleanup tears down the ephemeral workspace; its error is joined with the
	// run result so a cleanup failure surfaces as a failed CI run.
	defer func() { err = errors.Join(err, cleanup()) }()

	if _, ok := workspaceClient.(client.WorkspaceClient); !ok {
		return fmt.Errorf("ci is currently not supported for proxy providers")
	}

	if _, err := up.RunHeadless(ctx, workspaceClient, up.HeadlessOptions{
		GlobalFlags:        cmd.GlobalFlags,
		DevsyConfig:        devsyConfig,
		CLIOptions:         cmd.CLIOptions,
		ProviderOptions:    cmd.ProviderOptions,
		SecretsFile:        cmd.SecretsFile,
		FeatureSecretsFile: cmd.FeatureSecretsFile,
	}); err != nil {
		return fmt.Errorf("start devcontainer: %w", err)
	}

	log.Infof("running command in devcontainer: %s", strings.Join(cmd.RunCmd, " "))
	return cmd.runInContainer(ctx, workspaceClient)
}

// resolveWorkspace resolves (creating if needed) the workspace to run in. The
// returned cleanup tears down only workspaces this command created, and only
// when --keep was not passed.
func (cmd *CICmd) resolveWorkspace(
	ctx context.Context,
	devsyConfig *config.Config,
	source string,
) (client.BaseWorkspaceClient, func() error, error) {
	var args []string
	if source != "" {
		args = []string{source}
	}
	exists := workspace2.Exists(ctx, devsyConfig, args, "", cmd.Owner)

	sshConfigPath, cleanupSSH, err := workspacecmd.NewTempSSHConfig()
	if err != nil {
		return nil, nil, err
	}

	workspaceClient, err := workspace2.Resolve(ctx, devsyConfig, workspace2.ResolveParams{
		Args:                args,
		DesiredMachine:      cmd.Machine,
		ProviderUserOptions: cmd.ProviderOptions,
		DevContainerImage:   cmd.DevContainerImage,
		DevContainerPath:    cmd.DevContainerPath,
		SSHConfigPath:       sshConfigPath,
		UID:                 cmd.UID,
		Owner:               cmd.Owner,
	})
	if err != nil {
		cleanupSSH()
		return nil, nil, err
	}

	teardown := exists == "" && !cmd.Keep
	cleanup := func() error {
		defer cleanupSSH()
		if teardown {
			return cmd.teardown(workspaceClient)
		}
		return nil
	}
	return workspaceClient, cleanup, nil
}

// runInContainer runs the command inside the started container, reusing the
// exec command so it runs as the devcontainer's remote user.
func (cmd *CICmd) runInContainer(ctx context.Context, c client.BaseWorkspaceClient) error {
	execCmd := &workspacecmd.ExecCmd{
		GlobalFlags:   cmd.GlobalFlags,
		WorkspaceName: c.Workspace(),
		RemoteEnv:     cmd.RemoteEnv,
	}
	return execCmd.Run(ctx, cmd.RunCmd)
}

// teardown deletes the ephemeral workspace. It uses its own bounded context so
// teardown still runs after the command context was cancelled (e.g. by a
// cancelled CI job), which is the whole point of running it on the way out.
func (cmd *CICmd) teardown(c client.BaseWorkspaceClient) error {
	log.Infof("tearing down workspace")
	ctx, cancel := context.WithTimeout(context.Background(), teardownTimeout)
	defer cancel()
	if err := c.Delete(ctx, client.DeleteOptions{Force: true}); err != nil {
		return fmt.Errorf("delete workspace: %w", err)
	}
	return nil
}

func (cmd *CICmd) validateEnv() error {
	for _, env := range cmd.RemoteEnv {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 || parts[0] == "" {
			return fmt.Errorf("invalid %s value %q: must be KEY=VALUE format",
				names.Flag(names.RemoteEnv), env)
		}
	}
	return nil
}
