package cmd

import (
	gocontext "context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy/cmd/ci"
	"github.com/devsy-org/devsy/cmd/completion"
	cliconfig "github.com/devsy-org/devsy/cmd/config"
	"github.com/devsy-org/devsy/cmd/container"
	"github.com/devsy-org/devsy/cmd/context"
	envcmd "github.com/devsy-org/devsy/cmd/env"
	"github.com/devsy-org/devsy/cmd/feature"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/ide"
	cmdinternal "github.com/devsy-org/devsy/cmd/internal"
	"github.com/devsy-org/devsy/cmd/machine"
	"github.com/devsy-org/devsy/cmd/mcp"
	"github.com/devsy-org/devsy/cmd/platform"
	"github.com/devsy-org/devsy/cmd/provider"
	secretscmd "github.com/devsy-org/devsy/cmd/secrets"
	snapshotcmd "github.com/devsy-org/devsy/cmd/snapshot"
	"github.com/devsy-org/devsy/cmd/template"
	"github.com/devsy-org/devsy/cmd/update"
	wsCmdPkg "github.com/devsy-org/devsy/cmd/workspace"
	"github.com/devsy-org/devsy/logging"
	"github.com/devsy-org/devsy/logging/redact"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/clihelp"
	"github.com/devsy-org/devsy/pkg/command"
	"github.com/devsy-org/devsy/pkg/config"
	devcconfig "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/exitcode"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/flatpak"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/devsy-org/devsy/pkg/version"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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

// platformEnabled keeps the platform command tree off until DEVSY_PLATFORM_ENABLED=true;
// the feature is not ready for general use.
func platformEnabled() bool {
	return config.IsPlatformEnabled()
}

// isMachineLogFormat reports whether the configured --log-output mode produces
// a structured, machine-parseable stream (json or logfmt). Callers use this to
// suppress decorative human-readable affordances that would corrupt the stream.
func isMachineLogFormat(format string) bool {
	return format == logOutputJSON || format == logOutputLogfmt
}

func logOutputFromArgs(args []string) string {
	var format string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		for _, name := range []string{flagLogOutput, flagLogFormat} {
			if val, ok := strings.CutPrefix(arg, name+"="); ok {
				format = val
				break
			}
			if arg == name && i+1 < len(args) {
				i++
				format = args[i]
				break
			}
		}
	}
	return format
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
			"invalid log level, expected one of: %s",
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
	command.MarkInheritedFDsCloseOnExec()
	os.Exit(run())
}

func run() (code int) {
	machineMode := false
	recordInvocation := true
	metrics := telemetry.NewMetricsReporter()
	var finalErr error
	collector := telemetry.FromContext(gocontext.Background()) // noop until BootstrapCLI
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("panic: %v\n%s", r, debug.Stack())
			finalErr = clierr.NewPanic(r)
			code = exitCodeForError(finalErr, machineMode)
		}
		if recordInvocation {
			recordCLIMetrics(collector, metrics.Finish(finalErr), finalErr)
		}
		collector.Flush()
	}()

	rootCmd, globalFlags := BuildRoot()
	target := resolveTarget(rootCmd)
	collector = telemetry.BootstrapCLI(target)
	ctx := telemetry.WithCollector(gocontext.Background(), collector)
	rootCmd.SetContext(telemetry.WithMetrics(ctx, metrics))

	isInternal := topLevelCommand(target) == internalCommand
	machineMode = configureOutput(rootCmd, globalFlags, isInternal)

	if !isInternal {
		if shouldExit, err := flatpak.ReexecOnHost(); err != nil {
			finalErr = err
			return exitCodeForError(finalErr, machineMode)
		} else if shouldExit {
			recordInvocation = false
			return 0
		}
	}

	finalErr = rootCmd.Execute()
	if devsyConfig, cfgErr := config.LoadConfig(
		globalFlags.Context,
		globalFlags.Provider,
	); cfgErr == nil {
		collector = telemetry.ApplyCLIConfig(devsyConfig, collector)
	}

	if finalErr != nil {
		return exitCodeForError(finalErr, machineMode)
	}
	return 0
}

func recordCLIMetrics(
	collector telemetry.CLICollector,
	snapshot telemetry.MetricsSnapshot,
	err error,
) {
	collector.RecordCLI(err, snapshot)
	if err := telemetry.ExportMetrics(snapshot); err != nil {
		log.WithAttrs(log.AttrErrorCode("metrics_export_failed")).
			Error("local metrics export failed")
	}
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
	if logOutput == "" && rootCmd.PersistentFlags().Changed(names.LogFormat) {
		logOutput = globalFlags.LogOutput
	}
	logOutput = normalizeLogOutput(logOutput)
	machineMode := isMachineConsumer(logOutput, isInternal)
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
	log.Init(log.Config{
		Verbosity:    globalFlags.Verbosity,
		Quiet:        globalFlags.Quiet,
		Debug:        globalFlags.Debug,
		Level:        globalFlags.LogLevel,
		DefaultLevel: defaultLogLevel(globalFlags),
		VerbositySet: rootCmd.PersistentFlags().Changed(names.Verbose),
		Format:       format,
		Redactor:     redact.New(redact.Options{Environment: os.Environ()}),
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

	if code, ok := unsafeOutputExitCode(err); ok {
		return code
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

func unsafeOutputExitCode(err error) (int, bool) {
	unsafe, ok := errors.AsType[interface {
		error
		SkipFinalStatus() bool
	}](err)
	if !ok || !unsafe.SkipFinalStatus() {
		return 0, false
	}
	if commandExit, ok := errors.AsType[*workspace.CommandExitError](err); ok {
		return commandExit.Code, true
	}
	return exitcode.Failure, true
}

func commandExitCode(commandExit *workspace.CommandExitError, machineMode bool) int {
	if machineMode || commandExit.Cause != nil {
		renderCLIError(clierr.Classify(commandExit), machineMode)
	}
	return commandExit.Code
}

func passthroughExitCode(err error, machineMode bool) (int, bool) {
	if commandExit, ok := errors.AsType[*workspace.CommandExitError](err); ok {
		return commandExitCode(commandExit, machineMode), true
	}
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
	writeCLIErrorText(cliErr)
}

func writeCLIErrorText(cliErr *clierr.CLIError) {
	redactor := redact.New(redact.Options{Environment: os.Environ()})
	fmt.Fprintf(os.Stderr, "Error: %s\n", redactor.Redact(cliErr.Message))
	if cliErr.Code != "" && cliErr.Code != clierr.CodeUnknown {
		fmt.Fprintf(os.Stderr, "Error code: %s\n", redactor.Redact(string(cliErr.Code)))
	}
	writeCLIErrorContext(cliErr.Context, redactor)
	if cliErr.Hint != "" {
		fmt.Fprintf(os.Stderr, "Try: %s\n", redactor.Redact(cliErr.Hint))
	}
}

func writeCLIErrorContext(errContext map[string]string, redactor *redact.Redactor) {
	if len(errContext) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "Context:")
	keys := make([]string, 0, len(errContext))
	for key := range errContext {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		fmt.Fprintf(
			os.Stderr,
			"  %s: %s\n",
			redactor.Redact(key),
			redactor.Redact(errContext[key]),
		)
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

	rootCmd.PersistentPreRunE = func(cobraCmd *cobra.Command, _ []string) error {
		return rootPreRun(cobraCmd, globalFlags, persistentFlags)
	}
	rootCmd.PersistentPostRunE = func(_ *cobra.Command, _ []string) error {
		if globalFlags.DevsyHome != "" {
			_ = os.Unsetenv(config.EnvHome)
		}
		return nil
	}

	registerSubcommands(rootCmd, globalFlags)

	return rootCmd, globalFlags
}

func registerSubcommands(rootCmd *cobra.Command, globalFlags *flags.GlobalFlags) {
	rootCmd.AddCommand(
		ci.NewCICmd(globalFlags),
		cliconfig.NewConfigCmd(globalFlags),
		container.NewContainerCmd(globalFlags, rootCmd.PersistentPreRunE),
		context.NewContextCmd(globalFlags),
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
		rootCmd.AddCommand(platform.NewPlatformCmd(globalFlags))
	}
}

func rootPreRun(
	cobraCmd *cobra.Command,
	globalFlags *flags.GlobalFlags,
	persistentFlags *pflag.FlagSet,
) error {
	if err := validateLogLevel(globalFlags.LogLevel); err != nil {
		return err
	}
	if err := applyOutputControls(cobraCmd, globalFlags, persistentFlags); err != nil {
		return err
	}
	if globalFlags.DevsyHome != "" {
		_ = os.Setenv(config.EnvHome, globalFlags.DevsyHome)
	}
	if err := checkPendingContextDeletion(cobraCmd); err != nil {
		return err
	}
	cobraCmd.SilenceUsage = true

	log.Init(log.Config{
		Verbosity:    globalFlags.Verbosity,
		Quiet:        globalFlags.Quiet,
		Debug:        globalFlags.Debug,
		Level:        globalFlags.LogLevel,
		DefaultLevel: defaultLogLevel(globalFlags),
		VerbositySet: persistentFlags.Changed(names.Verbose),
		Format:       resolveLogFormat(cobraCmd, globalFlags.LogOutput),
		Redactor:     redact.New(redact.Options{Environment: os.Environ()}),
	})
	klog.SetLogger(logr.New(log.LogrSink()))

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

func applyOutputControls(
	cmd *cobra.Command,
	globals *flags.GlobalFlags,
	flags *pflag.FlagSet,
) error {
	if flags.Changed(names.LogFormat) && !flags.Changed(names.LogOutput) {
		if _, ok := logging.ParseFormat(globals.LogOutput); !ok {
			return errors.New("invalid log format, expected auto, text, plain, json, or logfmt")
		}
	}
	if !flags.Changed(names.ResultFormat) {
		return nil
	}
	mode, err := output.ResolveMode(globals.ResultFormat)
	if err != nil {
		return err
	}
	return setLegacyJSONOutput(cmd, mode == output.ModeJSON)
}

func setLegacyJSONOutput(cmd *cobra.Command, jsonOutput bool) error {
	legacy := cmd.Flags().Lookup(names.JSON)
	if legacy == nil || legacy.Changed || legacy.Value.Type() != "bool" {
		return nil
	}
	return legacy.Value.Set(strconv.FormatBool(jsonOutput))
}

// checkPendingContextDeletion blocks commands until a crashed deletion is
// resumed, because its stores are only partially cleaned. It reads only
// recovery metadata; the matching delete command validates and resumes under
// the config lock.
func checkPendingContextDeletion(cobraCmd *cobra.Command) error {
	parent := cobraCmd.Parent()
	isContextDelete := cobraCmd.Name() == "delete" && parent != nil &&
		parent.Name() == "context"
	if isContextDelete {
		return nil
	}
	return config.CheckPendingContextDeletion()
}

func resolveLogFormat(cobraCmd *cobra.Command, logFormat string) string {
	logFormat = normalizeLogOutput(logFormat)
	if logFormat != "" {
		return logFormat
	}
	if isMachineConsumer("", topLevelCommand(cobraCmd) == internalCommand) {
		return logOutputJSON
	}
	return logOutputText
}

func normalizeLogOutput(value string) string {
	switch strings.ToLower(value) {
	case "auto":
		return ""
	case "plain":
		return logOutputText
	default:
		return strings.ToLower(value)
	}
}
