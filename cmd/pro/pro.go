package pro

import (
	"context"
	"fmt"
	"os"

	"github.com/sirupsen/logrus"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/cmd/pro/add"
	"github.com/devsy-org/devsy/cmd/pro/daemon"
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/cmd/pro/provider"
	"github.com/devsy-org/devsy/cmd/pro/reset"
	"github.com/devsy-org/devsy/pkg/config"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/skevetter/log"
	"github.com/spf13/cobra"
)

// NewProCmd returns a new command.
func NewProCmd(flags *flags.GlobalFlags, streamLogger *log.StreamLogger) *cobra.Command {
	globalFlags := &proflags.GlobalFlags{GlobalFlags: flags}
	proCmd := &cobra.Command{
		Use:           "pro",
		Short:         "Devsy Pro commands",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PersistentPreRunE: func(c *cobra.Command, _ []string) error {
			globalFlags = proflags.SetGlobalFlags(c.PersistentFlags())
			if flags.Silent {
				streamLogger.SetLevel(logrus.FatalLevel)
			}
			if flags.Debug {
				streamLogger.SetLevel(logrus.DebugLevel)
			}

			if os.Getenv(config.EnvDebug) == config.BoolTrue {
				log.Default.SetLevel(logrus.DebugLevel)
			}
			if flags.LogOutput == "json" {
				log.Default.SetFormat(log.JSONFormat)
			}

			return nil
		},
	}

	proCmd.AddCommand(NewLoginCmd(globalFlags))
	proCmd.AddCommand(NewListCmd(globalFlags))
	proCmd.AddCommand(NewDeleteCmd(globalFlags))
	proCmd.AddCommand(NewImportCmd(globalFlags))
	proCmd.AddCommand(NewStartCmd(globalFlags))
	proCmd.AddCommand(NewRebuildCmd(globalFlags))
	proCmd.AddCommand(NewSleepCmd(globalFlags))
	proCmd.AddCommand(NewWakeupCmd(globalFlags))
	proCmd.AddCommand(reset.NewResetCmd(globalFlags))
	proCmd.AddCommand(provider.NewProProviderCmd(globalFlags))
	proCmd.AddCommand(daemon.NewCmd(globalFlags))
	proCmd.AddCommand(add.NewAddCmd(globalFlags))
	proCmd.AddCommand(NewWatchWorkspacesCmd(globalFlags))
	proCmd.AddCommand(NewSelfCmd(globalFlags))
	proCmd.AddCommand(NewVersionCmd(globalFlags))
	proCmd.AddCommand(NewListProjectsCmd(globalFlags))
	proCmd.AddCommand(NewListWorkspacesCmd(globalFlags))
	proCmd.AddCommand(NewListTemplatesCmd(globalFlags))
	proCmd.AddCommand(NewListClustersCmd(globalFlags))
	proCmd.AddCommand(NewCreateWorkspaceCmd(globalFlags))
	proCmd.AddCommand(NewUpdateWorkspaceCmd(globalFlags))
	proCmd.AddCommand(NewCheckHealthCmd(globalFlags))
	proCmd.AddCommand(NewCheckUpdateCmd(globalFlags))
	proCmd.AddCommand(NewUpdateProviderCmd(globalFlags))
	return proCmd
}

func findProProvider(
	ctx context.Context,
	context, provider, host string,
	log log.Logger,
) (*config.Config, *providerpkg.ProviderConfig, error) {
	devsyConfig, err := config.LoadConfig(context, provider)
	if err != nil {
		return nil, nil, err
	}

	pCfg, err := workspace.ProviderFromHost(ctx, devsyConfig, host, log)
	if err != nil {
		return devsyConfig, nil, fmt.Errorf("load provider: %w", err)
	}

	return devsyConfig, pCfg, nil
}
