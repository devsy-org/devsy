package cmd

import (
	gocontext "context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"

	"github.com/devsy-org/devsy/cmd/ci"
	"github.com/devsy-org/devsy/cmd/completion"
	cliconfig "github.com/devsy-org/devsy/cmd/config"
	"github.com/devsy-org/devsy/cmd/context"
	envcmd "github.com/devsy-org/devsy/cmd/env"
	"github.com/devsy-org/devsy/cmd/feature"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/ide"
	cmdinternal "github.com/devsy-org/devsy/cmd/internal"
	"github.com/devsy-org/devsy/cmd/machine"
	"github.com/devsy-org/devsy/cmd/mcp"
	"github.com/devsy-org/devsy/cmd/pro"
	"github.com/devsy-org/devsy/cmd/provider"
	secretscmd "github.com/devsy-org/devsy/cmd/secrets"
	"github.com/devsy-org/devsy/cmd/self"
	"github.com/devsy-org/devsy/cmd/template"
	wsCmdPkg "github.com/devsy-org/devsy/cmd/workspace"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/clihelp"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/exitcode"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/flatpak"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/devsy-org/devsy/pkg/version"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
	"k8s.io/klog/v2"
)

const (
	logOutputJSON   = "json"
	logOutputLogfmt = "logfmt"
	logOutputText   = "text"

	flagLogOutput = "--" + names.LogOutput
	flagLogFormat = "--" + names.LogFormat

	groupCore         = "core"
	groupConfig       = "config"
	groupPlatform     = "platform"
	groupDevcontainer = "devcontainer"
	groupMeta         = "meta"

	// envProEnabled gates registration of the `pro` command tree. The pro
	// feature is not ready for general use; set DEVSY_PRO_ENABLED=true to
	// expose it (e.g. for internal testing).
	envProEnabled = "DEVSY_PRO_ENABLED"

	internalCommand = "internal"

	rootLong = "Devsy — standardized development workspaces built on devcontainers, " +
		"running on Docker, Kubernetes, cloud providers, and SSH remote hosts."

	rootExample = `- Start a workspace from the current directory:

    $ devsy workspace up .

- Open an SSH session to a workspace:

    $ devsy workspace ssh my-workspace

- Configure a provider:

    $ devsy provider add docker`
)

func proEnabled() bool {
	return os.Getenv(envProEnabled) == "true"
}

// isMachineLogFormat reports whether the configured --log-output mode produces
// a structured, machine-parseable stream (json or logfmt). Callers use this to
// suppress decorative human-readable affordances that would corrupt the stream.
func isMachineLogFormat(format string) bool {
	return format == logOutputJSON || format == logOutputLogfmt
}

func logOutputFromArgs(args []string) string {
	for i, arg := range args {
		for _, name := range []string{flagLogOutput, flagLogFormat} {
			if val, ok := strings.CutPrefix(arg, name+"="); ok {
				return val
			}
			if arg == name && i+1 < len(args) {
				return args[i+1]
			}
		}
	}
	return ""
}

func isMachineConsumer(logOutput string, isInternal bool) bool {
	switch {
	case isInternal:
		return true
	case os.Getenv(config.EnvUI) == config.BoolTrue:
		return true
	case logOutput != "":
		return isMachineLogFormat(logOutput)
	default:
		return !term.IsTerminal(int(os.Stderr.Fd())) //nolint:gosec // fd fits in int
	}
}

func Execute() {
	os.Exit(run())
}

func run() (code int) {
	machineMode := false
	collector := telemetry.FromContext(gocontext.Background()) // noop until BootstrapCLI
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("panic: %v\n%s", r, debug.Stack())
			panicErr := clierr.NewPanic(r)
			collector.RecordCLI(panicErr)
			code = exitCodeForError(panicErr, machineMode)
		}
		collector.Flush()
	}()

	rootCmd, globalFlags := BuildRoot()
	target := resolveTarget(rootCmd)
	collector = telemetry.BootstrapCLI(target)
	rootCmd.SetContext(telemetry.WithCollector(gocontext.Background(), collector))

	isInternal := topLevelCommand(target) == internalCommand
	machineMode = configureOutput(rootCmd, globalFlags, isInternal)

	if !isInternal {
		if shouldExit, err := flatpak.ReexecOnHost(); err != nil {
			collector.RecordCLI(err)
			return exitCodeForError(err, machineMode)
		} else if shouldExit {
			return 0
		}
	}

	err := rootCmd.Execute()
	if devsyConfig, cfgErr := config.LoadConfig(
		globalFlags.Context,
		globalFlags.Provider,
	); cfgErr == nil {
		collector = telemetry.ApplyCLIConfig(devsyConfig, collector)
	}

	collector.RecordCLI(err)
	if err != nil {
		return exitCodeForError(err, machineMode)
	}
	return 0
}

func resolveTarget(rootCmd *cobra.Command) *cobra.Command {
	if found, _, err := rootCmd.Find(os.Args[1:]); err == nil && found != nil {
		return found
	}
	return rootCmd
}

func configureOutput(
	rootCmd *cobra.Command,
	globalFlags *flags.GlobalFlags,
	isInternal bool,
) bool {
	logOutput := logOutputFromArgs(os.Args[1:])
	machineMode := isMachineConsumer(logOutput, isInternal)
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = machineMode

	format := logOutput
	if format == "" {
		format = logOutputText
	}
	log.Init(log.Config{
		Verbosity: globalFlags.Verbosity,
		Quiet:     globalFlags.Quiet,
		Debug:     globalFlags.Debug,
		Format:    format,
	})
	return machineMode
}

func topLevelCommand(cmd *cobra.Command) string {
	if cmd == nil {
		return ""
	}
	for cmd.HasParent() {
		if !cmd.Parent().HasParent() {
			return cmd.Name()
		}
		cmd = cmd.Parent()
	}
	return ""
}

func exitCodeForError(err error, machineMode bool) int {
	if err == nil {
		return exitcode.Success
	}

	cliErr := clierr.Classify(err)

	// Stay transparent for unclassified child-process exits (e.g. `devsy ssh -- cmd`).
	if cliErr.Code == clierr.CodeUnknown {
		if code, ok := passthroughExitCode(err, machineMode); ok {
			return code
		}
	}

	renderCLIError(cliErr, machineMode)
	if errors.Is(err, workspace.ErrWorkspaceNotFound) {
		return exitcode.Retryable
	}
	if cliErr.Code == clierr.CodeBuildFailedRecoverable {
		return exitcode.BuildFailedRecoverable
	}
	return exitcode.Failure
}

func passthroughExitCode(err error, machineMode bool) (int, bool) {
	if sshExitErr, ok := errors.AsType[*ssh.ExitError](err); ok {
		if machineMode {
			log.Errorf("SSH command failed with exit code %d", sshExitErr.ExitStatus())
		}
		return sshExitErr.ExitStatus(), true
	}
	if execExitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		if machineMode {
			log.Errorf("Command failed with exit code %d", execExitErr.ExitCode())
		}
		return execExitErr.ExitCode(), true
	}
	return 0, false
}

func renderCLIError(cliErr *clierr.CLIError, machineMode bool) {
	if cliErr == nil {
		return
	}
	if machineMode {
		log.JSONError(cliErr)
		return
	}
	fmt.Fprintf(os.Stderr, "Error: %s\n", cliErr.Message)
}

// BuildRoot constructs the root command and returns it alongside the parsed
// global flags struct so callers (Execute, tests) can inspect parsed state
// without reaching for package-level mutable state.
func BuildRoot() (*cobra.Command, *flags.GlobalFlags) {
	rootCmd := &cobra.Command{
		Use:     config.BinaryName,
		Short:   "Devsy",
		Long:    rootLong,
		Example: rootExample,
		Version: version.GetVersion(),

		SilenceUsage:  true,
		SilenceErrors: true,
	}
	rootCmd.SetVersionTemplate("{{.Version}}\n")
	clihelp.Install(rootCmd)
	persistentFlags := rootCmd.PersistentFlags()
	globalFlags := flags.SetGlobalFlags(persistentFlags)
	_ = completion.RegisterFlagCompletionFuns(rootCmd, globalFlags)

	rootCmd.PersistentPreRunE = func(cobraCmd *cobra.Command, _ []string) error {
		cobraCmd.SilenceUsage = true

		log.Init(log.Config{
			Verbosity: globalFlags.Verbosity,
			Quiet:     globalFlags.Quiet,
			Debug:     globalFlags.Debug,
			Format:    globalFlags.LogOutput,
		})
		klog.SetLogger(logr.New(log.LogrSink()))

		if globalFlags.DevsyHome != "" {
			_ = os.Setenv(config.EnvHome, globalFlags.DevsyHome)
		}

		devsyConfig, err := config.LoadConfig(globalFlags.Context, globalFlags.Provider)
		if err == nil {
			current := telemetry.FromContext(cobraCmd.Context())
			cobraCmd.SetContext(telemetry.WithCollector(
				cobraCmd.Context(),
				telemetry.ApplyCLIConfig(devsyConfig, current),
			))
		}
		return nil
	}
	rootCmd.PersistentPostRunE = func(_ *cobra.Command, _ []string) error {
		if globalFlags.DevsyHome != "" {
			_ = os.Unsetenv(config.EnvHome)
		}
		return nil
	}

	registerGroups(rootCmd)
	registerSubcommands(rootCmd, globalFlags)

	return rootCmd, globalFlags
}

func registerGroups(rootCmd *cobra.Command) {
	groups := []*cobra.Group{
		{ID: groupCore, Title: "Core commands:"},
		{ID: groupConfig, Title: "Configuration commands:"},
		{ID: groupDevcontainer, Title: "Devcontainer commands:"},
		{ID: groupMeta, Title: "Meta:"},
	}
	if proEnabled() {
		groups = append(groups, &cobra.Group{ID: groupPlatform, Title: "Platform commands:"})
	}
	rootCmd.AddGroup(groups...)
}

func registerSubcommands(rootCmd *cobra.Command, globalFlags *flags.GlobalFlags) {
	providerCmd := provider.NewProviderCmd(globalFlags)
	providerCmd.GroupID = groupConfig
	rootCmd.AddCommand(providerCmd)
	ideCmd := ide.NewIDECmd(globalFlags)
	ideCmd.GroupID = groupConfig
	rootCmd.AddCommand(ideCmd)
	machineCmd := machine.NewMachineCmd(globalFlags)
	machineCmd.GroupID = groupCore
	rootCmd.AddCommand(machineCmd)
	contextCmd := context.NewContextCmd(globalFlags)
	contextCmd.GroupID = groupConfig
	rootCmd.AddCommand(contextCmd)
	secretsCmd := secretscmd.NewSecretsCmd(globalFlags)
	secretsCmd.GroupID = groupConfig
	rootCmd.AddCommand(secretsCmd)
	envCmd := envcmd.NewEnvCmd(globalFlags)
	envCmd.GroupID = groupConfig
	rootCmd.AddCommand(envCmd)
	if proEnabled() {
		proCmd := pro.NewProCmd(globalFlags)
		proCmd.GroupID = groupPlatform
		rootCmd.AddCommand(proCmd)
	}
	wsCmd := wsCmdPkg.NewWorkspaceCmd(globalFlags)
	wsCmd.GroupID = groupCore
	rootCmd.AddCommand(wsCmd)

	selfCmd := self.NewSelfCmd(globalFlags)
	selfCmd.GroupID = groupMeta
	rootCmd.AddCommand(selfCmd)
	mcpCmd := mcp.NewMCPCmd(globalFlags)
	mcpCmd.GroupID = groupMeta
	rootCmd.AddCommand(mcpCmd)
	configCmd := cliconfig.NewConfigCmd(globalFlags)
	configCmd.GroupID = groupDevcontainer
	rootCmd.AddCommand(configCmd)
	featureCmd := feature.NewFeatureCmd(globalFlags)
	featureCmd.GroupID = groupDevcontainer
	rootCmd.AddCommand(featureCmd)
	templateCmd := template.NewTemplateCmd(globalFlags)
	templateCmd.GroupID = groupDevcontainer
	rootCmd.AddCommand(templateCmd)
	ciCmd := ci.NewCICmd(globalFlags)
	ciCmd.GroupID = groupDevcontainer
	rootCmd.AddCommand(ciCmd)
	rootCmd.AddCommand(cmdinternal.NewInternalCmd(globalFlags))
}
