package cmd

import (
	gocontext "context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/devsy-org/devsy/cmd/ci"
	"github.com/devsy-org/devsy/cmd/completion"
	cliconfig "github.com/devsy-org/devsy/cmd/config"
	"github.com/devsy-org/devsy/cmd/context"
	"github.com/devsy-org/devsy/cmd/dependency"
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
	snapshotcmd "github.com/devsy-org/devsy/cmd/snapshot"
	"github.com/devsy-org/devsy/cmd/template"
	"github.com/devsy-org/devsy/cmd/update"
	wsCmdPkg "github.com/devsy-org/devsy/cmd/workspace"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/clihelp"
	"github.com/devsy-org/devsy/pkg/config"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/exitcode"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/flatpak"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/secrets"
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

	// envPlatformEnabled gates registration of the `pro` command tree. The pro
	// feature is not ready for general use; set DEVSY_PRO_ENABLED=true to
	// expose it (e.g. for internal testing).
	envPlatformEnabled = "DEVSY_PRO_ENABLED"

	internalCommand = "internal"

	rootLong = "Devsy - standardized development workspaces built on devcontainers, " +
		"running on Docker, Kubernetes, cloud providers, and SSH remote hosts."

	rootExample = `- Start a workspace from the current directory:

    $ devsy workspace up .

- Open an SSH session to a workspace:

    $ devsy workspace ssh my-workspace

- Configure a provider:

    $ devsy provider add docker`
)

func platformEnabled() bool {
	return os.Getenv(envPlatformEnabled) == "true"
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

func defaultLogLevel(globalFlags *flags.GlobalFlags) string {
	devsyConfig, err := config.LoadConfig(globalFlags.Context, globalFlags.Provider)
	if err != nil {
		return log.DefaultLevel
	}
	if level := devsyConfig.ContextOption(config.ContextOptionLogLevel); level != "" {
		return level
	}
	return log.DefaultLevel
}

func validateLogLevel(level string) error {
	if level == "" {
		return nil
	}
	if _, ok := log.LevelFromString(level); !ok {
		return fmt.Errorf(
			"invalid log level %q, expected one of: %s",
			level,
			strings.Join(log.ValidLevels(), ", "),
		)
	}
	return nil
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

func run() int {
	return runWithRootFactory(BuildRoot)
}

func runWithRootFactory(buildRoot func() (*cobra.Command, *flags.GlobalFlags)) (code int) {
	machineMode := false
	inspection := false
	collector := telemetry.FromContext(gocontext.Background()) // noop until BootstrapCLI
	defer func() {
		if r := recover(); r != nil {
			panicErr := invocationPanic(r, inspection)
			collector.RecordCLI(panicErr)
			code = exitCodeForError(panicErr, machineMode)
		}
		collector.Flush()
	}()

	rootCmd, globalFlags := buildRoot()
	target, remaining := resolveInvocationCommand(rootCmd, os.Args[1:])
	passive := passiveCommand(target, remaining)
	inspection = flags.IsDependencyInspection(target)
	invocation := readInspectionInvocation(target, remaining)
	if !passive {
		collector = telemetry.BootstrapCLI(target)
	}
	ctx := withInvocationPolicy(gocontext.Background(), passive)
	ctx = flags.WithInspectionInvocation(ctx, invocation)
	rootCmd.SetContext(telemetry.WithCollector(ctx, collector))

	isInternal := topLevelCommand(target) == internalCommand
	machineMode = configureOutput(rootCmd, globalFlags, isInternal)

	if !isInternal && !passive {
		if shouldExit, err := flatpak.ReexecOnHost(); err != nil {
			collector.RecordCLI(err)
			return exitCodeForError(err, machineMode)
		} else if shouldExit {
			return 0
		}
	}

	collector, err := executeRoot(rootCmd, globalFlags, collector)
	if err != nil {
		return exitCodeForError(err, machineMode)
	}
	return 0
}

func executeRoot(
	root *cobra.Command, globalFlags *flags.GlobalFlags, collector telemetry.CLICollector,
) (telemetry.CLICollector, error) {
	err := root.Execute()
	if !passiveInvocation(root, nil) {
		if snapshot, cfgErr := config.LoadConfig(
			globalFlags.Context,
			globalFlags.Provider,
		); cfgErr == nil {
			collector = telemetry.ApplyCLIConfig(snapshot, collector)
		}
	}
	collector.RecordCLI(err)
	return collector, err
}

func configureOutput(
	rootCmd *cobra.Command,
	globalFlags *flags.GlobalFlags,
	isInternal bool,
) bool {
	logOutput := logOutputFromArgs(os.Args[1:])
	invocation := flags.InspectionInvocationFromContext(rootCmd.Context())
	machineMode := invocation.JSON || isMachineConsumer(logOutput, isInternal)
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = machineMode

	format := logOutput
	if format == "" {
		if machineMode {
			format = logOutputJSON
		} else {
			format = logOutputText
		}
	}
	level := log.DefaultLevel
	if !passiveInvocation(rootCmd, nil) {
		level = defaultLogLevel(globalFlags)
	}
	log.Init(log.Config{
		Verbosity:    globalFlags.Verbosity,
		Quiet:        globalFlags.Quiet,
		Debug:        globalFlags.Debug,
		Level:        globalFlags.LogLevel,
		DefaultLevel: level,
		VerbositySet: rootCmd.PersistentFlags().Changed(names.Verbose),
		Format:       format,
		Redactor:     secrets.NewEnvironmentRedactor(os.Environ()),
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
			renderCLIError(
				clierr.Classify(fmt.Errorf(
					"SSH command failed with exit code %d",
					sshExitErr.ExitStatus(),
				)),
				true,
			)
		}
		return sshExitErr.ExitStatus(), true
	}
	if execExitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		if machineMode {
			renderCLIError(
				clierr.Classify(fmt.Errorf(
					"command failed with exit code %d",
					execExitErr.ExitCode(),
				)),
				true,
			)
		}
		return execExitErr.ExitCode(), true
	}
	if commandExit, ok := errors.AsType[*workspace.CommandExitError](err); ok {
		if machineMode {
			renderCLIError(clierr.Classify(commandExit), true)
		}
		return commandExit.Code, true
	}
	return 0, false
}

func renderCLIError(cliErr *clierr.CLIError, machineMode bool) {
	if cliErr == nil {
		return
	}
	if machineMode {
		if err := devcconfig.WriteCLIErrorJSON(os.Stderr, cliErr); err != nil {
			log.Errorf("failed to write CLI error envelope: %v", err)
		}
		return
	}
	redactor := secrets.NewEnvironmentRedactor(os.Environ())
	fmt.Fprintf(os.Stderr, "Error: %s\n", redactor.Redact(cliErr.Message))
	if cliErr.Code != "" && cliErr.Code != clierr.CodeUnknown {
		fmt.Fprintf(os.Stderr, "Error code: %s\n", redactor.Redact(string(cliErr.Code)))
	}
	if len(cliErr.Context) > 0 {
		fmt.Fprintln(os.Stderr, "Context:")
		keys := make([]string, 0, len(cliErr.Context))
		for key := range cliErr.Context {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			fmt.Fprintf(
				os.Stderr,
				"  %s: %s\n",
				redactor.Redact(key),
				redactor.Redact(cliErr.Context[key]),
			)
		}
	}
	if cliErr.Hint != "" {
		fmt.Fprintf(os.Stderr, "Try: %s\n", redactor.Redact(cliErr.Hint))
	}
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

	rootCmd.PersistentPreRunE = func(command *cobra.Command, args []string) error {
		return prepareRootCommand(command, args, globalFlags)
	}
	rootCmd.PersistentPostRunE = func(command *cobra.Command, args []string) error {
		if !passiveInvocation(command, args) && globalFlags.DevsyHome != "" {
			_ = os.Unsetenv(config.EnvHome)
		}
		return nil
	}

	registerSubcommands(rootCmd, globalFlags)

	return rootCmd, globalFlags
}

func prepareRootCommand(
	command *cobra.Command,
	args []string,
	globalFlags *flags.GlobalFlags,
) (err error) {
	defer func() { err = inspectionStartupError(command, err) }()
	if flags.IsDependencyInspection(command) {
		if err := command.Context().Err(); err != nil {
			return err
		}
	}

	if err := validateLogLevel(globalFlags.LogLevel); err != nil {
		return err
	}
	passive := passiveInvocation(command, args)
	level, err := prepareRootConfig(command, globalFlags, passive)
	if err != nil {
		return err
	}
	configureCommandOutput(command, globalFlags, level)
	if !passive {
		applyCommandTelemetry(command, globalFlags)
	}
	return nil
}

func prepareRootConfig(
	command *cobra.Command,
	globalFlags *flags.GlobalFlags,
	passive bool,
) (string, error) {
	if passive {
		return preparePassive(command, globalFlags)
	}
	if globalFlags.DevsyHome != "" {
		_ = os.Setenv(config.EnvHome, globalFlags.DevsyHome)
	}
	// A crashed deletion must be resumed before using its partially cleaned stores.
	// The matching delete command validates and resumes under the config lock.
	parent := command.Parent()
	isContextDelete := command.Name() == "delete" && parent != nil && parent.Name() == "context"
	if !isContextDelete {
		if err := config.CheckPendingContextDeletion(); err != nil {
			return "", err
		}
	}
	return defaultLogLevel(globalFlags), nil
}

func configureCommandOutput(command *cobra.Command, globalFlags *flags.GlobalFlags, level string) {
	command.SilenceUsage = true
	logFormat := globalFlags.LogOutput
	if logFormat == "" {
		if isMachineConsumer("", topLevelCommand(command) == internalCommand) {
			logFormat = logOutputJSON
		} else {
			logFormat = logOutputText
		}
	}
	log.Init(log.Config{
		Verbosity: globalFlags.Verbosity, Quiet: globalFlags.Quiet, Debug: globalFlags.Debug,
		Level: globalFlags.LogLevel, DefaultLevel: level,
		VerbositySet: command.Root().PersistentFlags().Changed(names.Verbose),
		Format:       logFormat, Redactor: secrets.NewEnvironmentRedactor(os.Environ()),
	})
	klog.SetLogger(logr.New(log.LogrSink()))
}

func applyCommandTelemetry(command *cobra.Command, globalFlags *flags.GlobalFlags) {
	snapshot, err := config.LoadConfig(globalFlags.Context, globalFlags.Provider)
	if err == nil {
		current := telemetry.FromContext(command.Context())
		command.SetContext(
			telemetry.WithCollector(command.Context(), telemetry.ApplyCLIConfig(snapshot, current)),
		)
	}
}

func registerSubcommands(rootCmd *cobra.Command, globalFlags *flags.GlobalFlags) {
	rootCmd.AddCommand(
		ci.NewCICmd(globalFlags),
		cliconfig.NewConfigCmd(globalFlags),
		context.NewContextCmd(globalFlags),
		dependency.NewDependencyCmd(globalFlags),
		envcmd.NewEnvCmd(globalFlags),
		feature.NewFeatureCmd(globalFlags),
		ide.NewIDECmd(globalFlags),
		machine.NewMachineCmd(globalFlags),
		mcp.NewMCPCmd(globalFlags),
		provider.NewProviderCmd(globalFlags),
		secretscmd.NewSecretsCmd(globalFlags),
		snapshotcmd.NewSnapshotCmd(globalFlags),
		template.NewTemplateCmd(globalFlags),
		update.NewUpdateCmd(),
		wsCmdPkg.NewWorkspaceCmd(globalFlags),
		cmdinternal.NewInternalCmd(globalFlags),
	)
	if platformEnabled() {
		rootCmd.AddCommand(pro.NewPlatformCmd(globalFlags))
	}
}
