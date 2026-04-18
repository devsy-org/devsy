package provider

import (
	"os"

	"github.com/devsy-org/devsy/cmd/agent"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/cmd/pro/provider/create"
	"github.com/devsy-org/devsy/cmd/pro/provider/get"
	"github.com/devsy-org/devsy/cmd/pro/provider/list"
	"github.com/devsy-org/devsy/cmd/pro/provider/update"
	"github.com/devsy-org/devsy/cmd/pro/provider/watch"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/skevetter/log"
	"github.com/spf13/cobra"
)

// NewProProviderCmd creates a new cobra command.
func NewProProviderCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	c := &cobra.Command{
		Use:    "provider",
		Short:  "Devsy Pro provider commands",
		Args:   cobra.NoArgs,
		Hidden: true,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if (globalFlags.Config == "" || globalFlags.Config == client.DefaultCacheConfig) &&
				os.Getenv("DEVSY_CONFIG") != "" {
				globalFlags.Config = os.Getenv(platform.ConfigEnv)
			}

			log.Default.SetFormat(log.JSONFormat)

			if os.Getenv(config.EnvDebug) == config.BoolTrue {
				globalFlags.Debug = true
			}

			// Disable debug hints if we execute pro commands from Devsy Desktop
			// We're reusing the agent.AgentExecutedAnnotation for simplicity, could rename in the future
			if os.Getenv(config.EnvUI) == config.BoolTrue {
				cmd.VisitParents(func(c *cobra.Command) {
					// find the root command
					if c.Name() == config.BinaryName {
						if c.Annotations == nil {
							c.Annotations = map[string]string{}
						}
						c.Annotations[agent.AgentExecutedAnnotation] = config.BoolTrue
					}
				})
			}
		},
	}

	c.AddCommand(list.NewCmd(globalFlags))
	c.AddCommand(watch.NewCmd(globalFlags))
	c.AddCommand(create.NewCmd(globalFlags))
	c.AddCommand(get.NewCmd(globalFlags))
	c.AddCommand(update.NewCmd(globalFlags))
	c.AddCommand(NewHealthCmd(globalFlags))

	c.AddCommand(NewUpCmd(globalFlags))
	c.AddCommand(NewStopCmd(globalFlags))
	c.AddCommand(NewSshCmd(globalFlags))
	c.AddCommand(NewStatusCmd(globalFlags))
	c.AddCommand(NewDeleteCmd(globalFlags))
	c.AddCommand(NewRebuildCmd(globalFlags))
	return c
}
