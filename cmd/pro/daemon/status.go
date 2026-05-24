package daemon

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/devsy-org/devsy/cmd/agent"
	"github.com/devsy-org/devsy/cmd/pro/completion"
	proflags "github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/config"
	platformdaemon "github.com/devsy-org/devsy/pkg/daemon/platform"
	providerpkg "github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// StatusCmd holds the Devsy daemon flags.
type StatusCmd struct {
	*proflags.GlobalFlags

	Host string
}

// NewStatusCmd creates a new command.
func NewStatusCmd(flags *proflags.GlobalFlags) *cobra.Command {
	cmd := &StatusCmd{
		GlobalFlags: flags,
	}
	c := &cobra.Command{
		Use:   "status",
		Short: "Get the status of the daemon",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, provider, err := findProProvider(
				cobraCmd.Context(),
				cmd.Context,
				cmd.Provider,
				cmd.Host,
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
	proflags.BindEnv(c.Flags(), "host")
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
			)
		},
	)

	return c
}

func (cmd *StatusCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *providerpkg.ProviderConfig,
) error {
	status, err := platformdaemon.NewLocalClient(provider.Name).Status(ctx, cmd.Debug)
	if err != nil {
		return err
	}
	out, err := json.Marshal(status)
	if err != nil {
		return err
	}

	fmt.Print(string(out))

	return nil
}
