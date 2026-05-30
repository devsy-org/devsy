package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/devsy-org/devsy/cmd/completion"
	cliconfig "github.com/devsy-org/devsy/cmd/config"
	"github.com/devsy-org/devsy/cmd/context"
	"github.com/devsy-org/devsy/cmd/feature"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/ide"
	cmdinternal "github.com/devsy-org/devsy/cmd/internal"
	"github.com/devsy-org/devsy/cmd/machine"
	"github.com/devsy-org/devsy/cmd/pro"
	"github.com/devsy-org/devsy/cmd/provider"
	"github.com/devsy-org/devsy/cmd/self"
	"github.com/devsy-org/devsy/cmd/template"
	wsCmdPkg "github.com/devsy-org/devsy/cmd/workspace"
	"github.com/devsy-org/devsy/cmd/workspace/up"
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
	logOutputJSON   = "json"
	logOutputLogfmt = "logfmt"

	groupCore         = "core"
	groupConfig       = "config"
	groupPlatform     = "platform"
	groupDevcontainer = "devcontainer"
	groupShortcut     = "shortcut"
	groupMeta         = "meta"
)

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

	err := rootCmd.Execute()
	telemetry.CollectorCLI.RecordCLI(err)
	telemetry.CollectorCLI.Flush()
	if err != nil {
		//nolint:all
		if sshExitErr, ok := err.(*ssh.ExitError); ok {
			log.Errorf("SSH command failed with exit code %d", sshExitErr.ExitStatus())
			os.Exit(sshExitErr.ExitStatus())
		}

		//nolint:all
		if execExitErr, ok := err.(*exec.ExitError); ok {
			log.Errorf("Command failed with exit code %d", execExitErr.ExitCode())
			os.Exit(execExitErr.ExitCode())
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
		if !isMachineLogFormat(globalFlags.LogOutput) {
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
			os.Exit(exitcode.WorkspaceNotFound)
		}
		os.Exit(1)
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
			telemetry.StartCLI(devsyConfig, cobraCmd)
		}
		return nil
	}
	rootCmd.PersistentPostRunE = func(_ *cobra.Command, _ []string) error {
		if globalFlags.DevsyHome != "" {
			_ = os.Unsetenv(config.EnvHome)
		}
		return nil
	}

	rootCmd.AddGroup(
		&cobra.Group{ID: groupCore, Title: "Core commands:"},
		&cobra.Group{ID: groupConfig, Title: "Configuration commands:"},
		&cobra.Group{ID: groupPlatform, Title: "Platform commands:"},
		&cobra.Group{ID: groupDevcontainer, Title: "Devcontainer commands:"},
		&cobra.Group{ID: groupShortcut, Title: "Workspace shortcuts:"},
		&cobra.Group{ID: groupMeta, Title: "Meta:"},
	)

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
	proCmd := pro.NewProCmd(globalFlags)
	proCmd.GroupID = groupPlatform
	rootCmd.AddCommand(proCmd)

	wsCmd := wsCmdPkg.NewWorkspaceCmd(globalFlags)
	wsCmd.GroupID = groupCore
	rootCmd.AddCommand(wsCmd)

	addShortcut := func(c *cobra.Command) {
		c.GroupID = groupShortcut
		rootCmd.AddCommand(c)
	}
	addShortcut(up.NewUpCmd(globalFlags))
	addShortcut(wsCmdPkg.NewStopCmd(globalFlags))
	addShortcut(wsCmdPkg.NewSSHCmd(globalFlags))
	addShortcut(wsCmdPkg.NewExecCmd(globalFlags))
	addShortcut(wsCmdPkg.NewLogsCmd(globalFlags))
	addShortcut(wsCmdPkg.NewStatusCmd(globalFlags))
	addShortcut(wsCmdPkg.NewListCmd(globalFlags))

	selfCmd := self.NewSelfCmd(globalFlags)
	selfCmd.GroupID = groupMeta
	rootCmd.AddCommand(selfCmd)
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
