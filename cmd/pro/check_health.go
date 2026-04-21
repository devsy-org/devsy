package pro

import (
	"bytes"
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/agent"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	devsylog "github.com/devsy-org/devsy/pkg/log"
	"github.com/spf13/cobra"
)

// CheckHealthCmd holds the cmd flags.
type CheckHealthCmd struct {
	*flags.GlobalFlags

	Host string
}

// NewCheckHealthCmd creates a new command.
func NewCheckHealthCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &CheckHealthCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "check-health",
		Short:  "Check platform health",
		Hidden: true,
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

	return c
}

func (cmd *CheckHealthCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider.ProviderConfig,
) error {
	var buf bytes.Buffer

	err := clientimplementation.RunCommandWithBinaries(clientimplementation.CommandOptions{
		Ctx:     ctx,
		Name:    "health",
		Command: provider.Exec.Proxy.Health,
		Context: devsyConfig.DefaultContext,
		Options: devsyConfig.ProviderOptions(provider.Name),
		Config:  provider,
		Stdout:  &buf,
		Stderr:  devsylog.Writer(devsylog.LevelError),
	})
	if err != nil {
		return fmt.Errorf("check health with provider \"%s\": %w", provider.Name, err)
	}

	fmt.Println(buf.String())

	return nil
}
