package cmd

import (
	"os"
	"os/exec"
	"strings"

	"github.com/devsy-org/devsy/cmd/agent"
	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/context"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/helper"
	"github.com/devsy-org/devsy/cmd/ide"
	"github.com/devsy-org/devsy/cmd/machine"
	"github.com/devsy-org/devsy/cmd/pro"
	"github.com/devsy-org/devsy/cmd/provider"
	"github.com/devsy-org/devsy/cmd/use"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/telemetry"
	"github.com/spf13/cobra"
	flag "github.com/spf13/pflag"
	"golang.org/x/crypto/ssh"
)

var globalFlags *flags.GlobalFlags

// NewRootCmd returns a new root command.
func NewRootCmd() *cobra.Command {
	return &cobra.Command{
		Use:           config.BinaryName,
		Short:         "Devsy",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cobraCmd *cobra.Command, args []string) error {
			log.Init(log.Config{
				Verbosity: globalFlags.Verbosity,
				Quiet:     globalFlags.Quiet,
				Debug:     globalFlags.Debug || os.Getenv(config.EnvDebug) == config.BoolTrue,
				Format:    globalFlags.LogOutput,
			})

			if globalFlags.DevsyHome != "" {
				_ = os.Setenv(config.EnvHome, globalFlags.DevsyHome)
			}

			devsyConfig, err := config.LoadConfig(globalFlags.Context, globalFlags.Provider)
			if err == nil {
				telemetry.StartCLI(devsyConfig, cobraCmd)
			}

			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			if globalFlags.DevsyHome != "" {
				_ = os.Unsetenv(config.EnvHome)
			}

			return nil
		},
	}
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	// build the root command
	rootCmd := BuildRoot()

	// execute command
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

		if globalFlags.Debug {
			log.Errorf("%+v", err)
		} else {
			if rootCmd.Annotations == nil ||
				rootCmd.Annotations[agent.AgentExecutedAnnotation] != config.BoolTrue {
				log.Error("Try using -v or --debug flag to see more verbose output")
			}
			log.Errorf("%v", err)
		}
		os.Exit(1)
	}
}

// BuildRoot creates a new root command from the.
func BuildRoot() *cobra.Command {
	rootCmd := NewRootCmd()
	persistentFlags := rootCmd.PersistentFlags()
	globalFlags = flags.SetGlobalFlags(persistentFlags)
	_ = completion.RegisterFlagCompletionFuns(rootCmd, globalFlags)

	rootCmd.AddCommand(agent.NewAgentCmd(globalFlags))
	rootCmd.AddCommand(provider.NewProviderCmd(globalFlags))
	rootCmd.AddCommand(use.NewUseCmd(globalFlags))
	rootCmd.AddCommand(helper.NewHelperCmd(globalFlags))
	rootCmd.AddCommand(ide.NewIDECmd(globalFlags))
	rootCmd.AddCommand(machine.NewMachineCmd(globalFlags))
	rootCmd.AddCommand(context.NewContextCmd(globalFlags))
	rootCmd.AddCommand(pro.NewProCmd(globalFlags))
	rootCmd.AddCommand(NewUpCmd(globalFlags))
	rootCmd.AddCommand(NewDeleteCmd(globalFlags))
	rootCmd.AddCommand(NewSSHCmd(globalFlags))
	rootCmd.AddCommand(NewVersionCmd())
	rootCmd.AddCommand(NewStopCmd(globalFlags))
	rootCmd.AddCommand(NewDownCmd(globalFlags))
	rootCmd.AddCommand(NewListCmd(globalFlags))
	rootCmd.AddCommand(NewStatusCmd(globalFlags))
	rootCmd.AddCommand(NewBuildCmd(globalFlags))
	rootCmd.AddCommand(NewLogsDaemonCmd(globalFlags))
	rootCmd.AddCommand(NewExportCmd(globalFlags))
	rootCmd.AddCommand(NewImportCmd(globalFlags))
	rootCmd.AddCommand(NewLogsCmd(globalFlags))
	rootCmd.AddCommand(NewUpgradeCmd())
	rootCmd.AddCommand(NewTroubleshootCmd(globalFlags))
	rootCmd.AddCommand(NewPingCmd(globalFlags))
	rootCmd.AddCommand(NewReadConfigurationCmd(globalFlags))
	rootCmd.AddCommand(NewExecCmd(globalFlags))

	inheritCommandFlagsFromEnvironment(rootCmd)

	return rootCmd
}

func inheritCommandFlagsFromEnvironment(cmd *cobra.Command) {
	inheritFlagsFromEnvironment(cmd.Flags())
	inheritFlagsFromEnvironment(cmd.PersistentFlags())

	for _, sub := range cmd.Commands() {
		inheritCommandFlagsFromEnvironment(sub)
	}
}

// Inherits default values for all flags that have a corresponding environment variable set.
func inheritFlagsFromEnvironment(flags *flag.FlagSet) {
	flags.VisitAll(func(flag *flag.Flag) {
		// calculate environment variable name from flag name
		suffix := strings.ToUpper(strings.ReplaceAll(flag.Name, "-", "_"))

		// do not prepend the env prefix if the flag name already starts with it
		// (applies to one flag - "devsy-home").
		var environmentVariable string
		if strings.HasPrefix(suffix, config.EnvPrefix) {
			environmentVariable = suffix
		} else {
			environmentVariable = config.EnvPrefix + suffix
		}

		if value, exists := os.LookupEnv(environmentVariable); exists {
			// set the variable holding the flag's value to the default supplied by the environment
			err := flag.Value.Set(value)
			if err != nil {
				log.Fatalf(
					"failed to set flag %s from the environment variable %s with value %s: %+v",
					flag.Name,
					environmentVariable,
					value,
					err,
				)
			}
			// reflect this default in the usage output
			flag.DefValue = value
		}

		// add note about environment variable to usage, but only if it is not there yet -
		// in case we visit the same flag more than once.
		usageAddition := ". You can also use " + environmentVariable + " to set this"
		if !strings.HasSuffix(flag.Usage, usageAddition) {
			flag.Usage = flag.Usage + usageAddition
		}
	})
}
