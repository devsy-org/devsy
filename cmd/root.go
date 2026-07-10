package cmd

import (
	gocontext "context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/devsy-org/devsy/cmd/completion"
	cliconfig "github.com/devsy-org/devsy/cmd/config"
	"github.com/devsy-org/devsy/cmd/context"
	"github.com/devsy-org/devsy/cmd/feature"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/ide"
	cmdinternal "github.com/devsy-org/devsy/cmd/internal"
	"github.com/devsy-org/devsy/cmd/machine"
	"github.com/devsy-org/devsy/cmd/mcp"
	"github.com/devsy-org/devsy/cmd/pro"
	"github.com/devsy-org/devsy/cmd/provider"
	"github.com/devsy-org/devsy/cmd/self"
	"github.com/devsy-org/devsy/cmd/template"
	wsCmdPkg "github.com/devsy-org/devsy/cmd/workspace"
	"github.com/devsy-org/devsy/pkg/config"
	cliErrors "github.com/devsy-org/devsy/pkg/errors"
	"github.com/devsy-org/devsy/pkg/exitcode"
	"github.com/devsy-org/devsy/pkg/flatpak"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/devsy-org/devsy/pkg/version"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"golang.org/x/crypto/ssh"
	"k8s.io/klog/v2"
)

const (
	logOutputJSON   = "json"
	logOutputLogfmt = "logfmt"
	logOutputText   = "text"

	flagLogOutput = "--log-output"
	flagLogFormat = "--log-format"

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

// logOutputFromArgs extracts the --log-output / --log-format value from raw args
// before cobra binds the persistent flags. Returns "text" when absent.
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
	return logOutputText
}

func Execute() {
	os.Exit(run())
}

// run builds and executes the root command, returning the process exit code.
func run() int {
	rootCmd, globalFlags := BuildRoot()
	target := rootCmd
	if found, _, findErr := rootCmd.Find(os.Args[1:]); findErr == nil && found != nil {
		target = found
	}
	collector := telemetry.BootstrapCLI(target)
	rootCmd.SetContext(telemetry.WithCollector(gocontext.Background(), collector))
	defer func() { collector.Flush() }()

	// Cobra prints its own error/usage in text mode; machine mode stays silent
	// so only the structured cliError reaches the stream.
	logOutput := logOutputFromArgs(os.Args[1:])
	machineMode := isMachineLogFormat(logOutput)
	rootCmd.SilenceErrors = machineMode
	rootCmd.SilenceUsage = machineMode

	// Initialize logging before Execute so errors on paths that skip
	// PersistentPreRunE (unknown command, flag parse error) still surface.
	log.Init(log.Config{
		Verbosity: globalFlags.Verbosity,
		Quiet:     globalFlags.Quiet,
		Debug:     globalFlags.Debug,
		Format:    logOutput,
	})

	if topLevelCommand(target) != internalCommand {
		if shouldExit, err := flatpak.ReexecOnHost(); err != nil {
			collector.RecordCLI(err)
			return exitCodeForError(err, logOutput)
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
		return exitCodeForError(err, logOutput)
	}
	return 0
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

// exitCodeForError renders err and returns the process exit code that reflects
// the failure.
func exitCodeForError(err error, logOutput string) int {
	if err == nil {
		return 0
	}

	machineMode := isMachineLogFormat(logOutput)
	if code, ok := passthroughExitCode(err, machineMode); ok {
		return code
	}

	renderCLIError(err, machineMode)
	if errors.Is(err, workspace.ErrWorkspaceNotFound) {
		return exitcode.WorkspaceNotFound
	}
	return 1
}

// passthroughExitCode returns the exit status of a subprocess or SSH command
// error verbatim. The second result is false when err is neither.
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

// renderCLIError emits the structured cliError in machine mode; in text mode
// cobra already printed the error line, so only hint/doc affordances are added.
func renderCLIError(err error, machineMode bool) {
	cliErr := cliErrors.Classify(err, cliErrors.ClassifyContext{})
	if machineMode {
		log.JSONError(cliErr)
		return
	}
	if cliErr.Hint != "" {
		fmt.Fprintf(os.Stderr, "Hint:  %s\n", cliErr.Hint)
	}
	if cliErr.DocURL != "" {
		fmt.Fprintf(os.Stderr, "See:   %s\n", cliErr.DocURL)
	}
}

// BuildRoot constructs the root command and returns it alongside the parsed
// global flags struct so callers (Execute, tests) can inspect parsed state
// without reaching for package-level mutable state.
func BuildRoot() (*cobra.Command, *flags.GlobalFlags) {
	rootCmd := &cobra.Command{
		Use:           config.BinaryName,
		Short:         "Devsy",
		Version:       version.GetVersion(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	rootCmd.SetVersionTemplate("{{.Version}}\n")
	persistentFlags := rootCmd.PersistentFlags()
	globalFlags := flags.SetGlobalFlags(persistentFlags)
	_ = completion.RegisterFlagCompletionFuns(rootCmd, globalFlags)

	rootCmd.PersistentPreRunE = func(cobraCmd *cobra.Command, _ []string) error {
		// Args parsed cleanly by now, so any later error is a runtime failure:
		// suppress cobra's usage dump. Parse errors occur before this hook.
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

	registerSubcommands(rootCmd, globalFlags)

	return rootCmd, globalFlags
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
	rootCmd.AddCommand(cmdinternal.NewInternalCmd(globalFlags))
}
