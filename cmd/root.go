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
	logOutputText   = "text"
	logOutputJSON   = "json"
	logOutputLogfmt = "logfmt"

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

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	rootCmd, globalFlags := BuildRoot()

	// Parse/flag errors return before PersistentPreRunE runs log.Init, so
	// initialize up front or reportError logs to a no-op logger. log.Init is
	// idempotent; PersistentPreRunE re-inits with the parsed flags. The same
	// raw-args format feeds reportError, since globalFlags.LogOutput is left
	// unpopulated when cobra fails during flag parsing.
	logOutput := parseLogOutputFlag(os.Args[1:])
	log.Init(log.Config{Format: logOutput})

	// Bootstrap pre-Execute so subcommands that override PersistentPreRunE
	// without chaining (e.g. pro, agent) still get telemetry.
	target := rootCmd
	if found, _, findErr := rootCmd.Find(os.Args[1:]); findErr == nil && found != nil {
		target = found
	}
	collector := telemetry.BootstrapCLI(target)
	rootCmd.SetContext(telemetry.WithCollector(gocontext.Background(), collector))

	err := rootCmd.Execute()

	// Re-apply opt-out post-Execute for the same PreRunE-bypass case.
	if devsyConfig, cfgErr := config.LoadConfig(
		globalFlags.Context,
		globalFlags.Provider,
	); cfgErr == nil {
		collector = telemetry.ApplyCLIConfig(devsyConfig, collector)
	}

	collector.RecordCLI(err)
	collector.Flush()
	if err != nil {
		os.Exit(reportError(err, logOutput))
	}
}

// reportError emits a command error through the logger and returns the process
// exit code. Split out from Execute so it is unit testable without os.Exit.
func reportError(err error, logOutput string) int {
	var sshExitErr *ssh.ExitError
	if errors.As(err, &sshExitErr) {
		log.Errorf("SSH command failed with exit code %d", sshExitErr.ExitStatus())
		return sshExitErr.ExitStatus()
	}

	var execExitErr *exec.ExitError
	if errors.As(err, &execExitErr) {
		log.Errorf("Command failed with exit code %d", execExitErr.ExitCode())
		return execExitErr.ExitCode()
	}

	cliErr := cliErrors.Classify(err, cliErrors.ClassifyContext{})
	// Always emit the error through zap so the configured log encoder
	// (json/logfmt/text) governs the wire format. JSONError preserves
	// the full err.Error() chain in the top-level "msg" field and ships
	// the structured CLIError under "cliError" for the desktop IPC.
	log.JSONError(cliErr)
	// In human-friendly text mode, follow up with hint/doc affordances
	// that don't fit cleanly into the zap line. These extras are
	// suppressed in machine-readable modes so log streams stay parseable.
	if !isMachineLogFormat(logOutput) {
		if cliErr.Hint != "" {
			fmt.Fprintf(os.Stderr, "Hint:  %s\n", cliErr.Hint)
		}
		if cliErr.DocURL != "" {
			fmt.Fprintf(os.Stderr, "See:   %s\n", cliErr.DocURL)
		}
	}
	// Signal workspace-not-found via a distinct exit code so parent
	// processes (e.g. SetupBackhaul) can detect the registration race
	// without parsing stderr.
	if errors.Is(err, workspace.ErrWorkspaceNotFound) {
		return exitcode.WorkspaceNotFound
	}
	return 1
}

// parseLogOutputFlag scans raw CLI args for --log-output / --log-format (and
// their =value forms) so the logger can be initialized with the right encoder
// before cobra parses flags. Unknown values fall through to the default text
// encoder via resolveEncoder.
func parseLogOutputFlag(args []string) string {
	for i, arg := range args {
		name, value, hasValue := strings.Cut(arg, "=")
		if name != flagLogOutput && name != flagLogFormat {
			continue
		}
		if hasValue {
			return value
		}
		if i+1 < len(args) {
			return args[i+1]
		}
	}
	return logOutputText
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
