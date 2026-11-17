package cmd

import (
	gocontext "context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime/debug"
	"slices"
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
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
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
	"github.com/devsy-org/devsy/pkg/secrets"
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
	logOutputPlain  = "plain"
	logOutputText   = "text"

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

func isMachineLogFormat(format string) bool {
	return format == logOutputJSON || format == logOutputLogfmt
}

func logOutputFromArgs(args []string) string {
	var value string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		if val, ok := strings.CutPrefix(arg, flagLogFormat+"="); ok {
			value = val
		} else if arg == flagLogFormat && i+1 < len(args) {
			i++
			value = args[i]
		}
	}
	return value
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
	case isMachineLogFormat(logOutput):
		return true
	case logOutput == "text" || logOutput == logOutputPlain:
		return false
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
	metrics := telemetry.NewMetricsReporter()
	var finalErr error
	collector := telemetry.FromContext(gocontext.Background()) // noop until BootstrapCLI
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("panic: %v\n%s", r, debug.Stack())
			finalErr = clierr.NewPanic(r)
			code = exitCodeForError(finalErr, machineMode)
		}
		snapshot := metrics.Finish(finalErr)
		collector.RecordCLI(finalErr, snapshot)
		if err := telemetry.ExportMetrics(snapshot); err != nil {
			log.WithAttrs(log.AttrErrorCode("metrics_export_failed")).
				Error("local metrics export failed")
		}
		collector.Flush()
	}()

	earlyFlags := &flags.GlobalFlags{
		LogLevel:  os.Getenv(config.EnvLogLevel),
		LogFormat: os.Getenv("DEVSY_LOG_FORMAT"),
	}
	machineMode = configureOutput(
		&cobra.Command{},
		earlyFlags,
		slices.Contains(os.Args[1:], internalCommand),
	)

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
	if logOutput == "" {
		logOutput = globalFlags.LogFormat
	}
	machineMode := isMachineConsumer(logOutput, isInternal)
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = machineMode

	format := logOutput
	if format == logOutputPlain {
		format = logOutputText
	}
	if format != logOutputText && format != logOutputJSON && format != logOutputLogfmt {
		if machineMode {
			format = logOutputJSON
		} else {
			format = logOutputText
		}
	}
	log.Init(log.Config{
		Level:        globalFlags.LogLevel,
		DefaultLevel: defaultLogLevel(globalFlags),
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
	redactor := secrets.NewEnvironmentRedactor(os.Environ())
	fmt.Fprintf(os.Stderr, "Error: %s\n", redactor.Redact(cliErr.Message))
	if cliErr.Code != "" && cliErr.Code != clierr.CodeUnknown {
		fmt.Fprintf(os.Stderr, "Error code: %s\n", redactor.Redact(string(cliErr.Code)))
	}
	writeCLIErrorContext(cliErr.Context, redactor)
	if cliErr.Hint != "" {
		fmt.Fprintf(os.Stderr, "Try: %s\n", redactor.Redact(cliErr.Hint))
	}
}

func writeCLIErrorContext(errContext map[string]string, redactor *secrets.Redactor) {
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
	rootCmd.Flags().Bool("version", false, "version for "+rootCmd.DisplayName())
	_ = rootCmd.Flags().SetAnnotation("version", cobra.FlagSetByCobraAnnotation, []string{"true"})
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
	for _, item := range []struct {
		name  string
		value string
	}{
		{names.LogLevel, globalFlags.LogLevel},
		{names.LogFormat, globalFlags.LogFormat},
		{names.ResultFormat, globalFlags.ResultFormat},
	} {
		if item.value == "" && persistentFlags.Changed(item.name) {
			return fmt.Errorf("--%s cannot be empty", item.name)
		}
	}
	parent := cobraCmd.Parent()
	if globalFlags.LogLevel == "" && !persistentFlags.Changed(names.LogLevel) &&
		cobraCmd.Name() == "up" && parent != nil && parent.Name() == "workspace" &&
		parent.Parent() == cobraCmd.Root() {
		var payload struct {
			LogLevel string `json:"logLevel"`
		}
		if _, err := clientimplementation.DecodeOptionsFromEnv(
			config.EnvFlagsUp,
			&payload,
		); err != nil {
			return fmt.Errorf("decode up options: %w", err)
		}
		globalFlags.LogLevel = payload.LogLevel
	}
	if err := validateLogLevel(globalFlags.LogLevel); err != nil {
		return err
	}
	switch globalFlags.LogFormat {
	case "", "auto", "text", logOutputPlain, "json", "logfmt":
	default:
		return errors.New("invalid log format, expected auto, text, plain, json, or logfmt")
	}
	if _, err := output.ResolveMode(globalFlags.ResultFormat); err != nil {
		return err
	}
	if globalFlags.DevsyHome != "" {
		_ = os.Setenv(config.EnvHome, globalFlags.DevsyHome)
	}
	defaultLevel := defaultLogLevel(globalFlags)
	if globalFlags.LogLevel == "" {
		if err := validateLogLevel(defaultLevel); err != nil {
			return err
		}
	}
	if err := checkPendingContextDeletion(cobraCmd); err != nil {
		return err
	}
	cobraCmd.SilenceUsage = true

	log.Init(log.Config{
		Level:        globalFlags.LogLevel,
		DefaultLevel: defaultLevel,
		Format:       resolveLogFormat(cobraCmd, globalFlags.LogFormat),
		Redactor:     secrets.NewEnvironmentRedactor(os.Environ()),
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
	if logFormat == logOutputPlain {
		return logOutputText
	}
	if logFormat != "" && logFormat != "auto" {
		return logFormat
	}
	if isMachineConsumer("", topLevelCommand(cobraCmd) == internalCommand) {
		return logOutputJSON
	}
	return logOutputText
}
