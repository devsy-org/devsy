package daemon

import (
	"context"
	"fmt"
	"strconv"

	"github.com/devsy-org/devsy/cmd/agent"
	"github.com/devsy-org/devsy/cmd/pro/completion"
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	daemon "github.com/devsy-org/devsy/pkg/daemon/platform"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/log"
	"github.com/spf13/cobra"
	"tailscale.com/client/local"
)

// NetcheckCmd holds the Devsy daemon flags.
type NetcheckCmd struct {
	*proflags.GlobalFlags

	Host string
	Log  log.Logger
}

// NewNetcheckCmd creates a new command.
func NewNetcheckCmd(flags *proflags.GlobalFlags) *cobra.Command {
	cmd := &NetcheckCmd{
		GlobalFlags: flags,
		Log:         log.Default,
	}
	c := &cobra.Command{
		Use:   "netcheck",
		Short: "Get the status of the current network",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, provider, err := findProProvider(
				cobraCmd.Context(),
				cmd.Context,
				cmd.Provider,
				cmd.Host,
				cmd.Log,
			)
			if err != nil {
				return err
			}

			return cmd.Run(cobraCmd.Context(), devsyConfig, provider)
		},
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			root := cmd.Root()
			if root == nil {
				return
			}
			if root.Annotations == nil {
				root.Annotations = map[string]string{}
			}
			// Don't print debug message
			root.Annotations[agent.AgentExecutedAnnotation] = "true"
		},
	}

	c.Flags().StringVar(&cmd.Host, "host", "", "The pro instance to use")
	_ = c.MarkFlagRequired("host")
	_ = c.RegisterFlagCompletionFunc(
		"host",
		func(rootCmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return completion.GetPlatformHostSuggestions(
				rootCmd,
				cmd.Context,
				cmd.Provider,
				args,
				toComplete,
				cmd.Owner,
				cmd.Log,
			)
		},
	)

	return c
}

func (cmd *NetcheckCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *providerpkg.ProviderConfig,
) error {
	tsClient := &local.Client{
		Socket:        daemon.GetSocketAddr(provider.Name),
		UseSocketOnly: true,
	}

	dm, err := tsClient.CurrentDERPMap(ctx)
	if err != nil {
		return err
	}

	for _, region := range dm.Regions {
		report, err := tsClient.DebugDERPRegion(ctx, strconv.Itoa(region.RegionID))
		if err != nil {
			return err
		}
		msg := fmt.Sprintf("DERP %d (%s)\n", region.RegionID, region.RegionCode)
		if len(report.Errors) > 0 {
			for _, error := range report.Errors {
				msg += fmt.Sprintf("  Error: %s\n", error)
			}
		}
		if len(report.Warnings) > 0 {
			for _, warning := range report.Warnings {
				msg += fmt.Sprintf("  Warning: %s\n", warning)
			}
		}
		if len(report.Info) > 0 {
			for _, info := range report.Info {
				msg += fmt.Sprintf("  Info: %s\n", info)
			}
		}
		fmt.Println(msg)
	}

	return nil
}
