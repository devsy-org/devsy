package pro

import (
	"bytes"
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// ListProjectsCmd holds the cmd flags.
type ListProjectsCmd struct {
	*flags.GlobalFlags

	Host string
}

// NewListProjectsCmd creates a new command.
func NewListProjectsCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListProjectsCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "list-projects",
		Short:  "List projects",
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
	}

	c.Flags().StringVar(&cmd.Host, "host", "", "The pro instance to use")
	_ = c.MarkFlagRequired("host")
	flags.BindEnv(c.Flags(), "host")

	return c
}

func (cmd *ListProjectsCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider.ProviderConfig,
) error {
	var buf bytes.Buffer

	err := clientimplementation.RunCommandWithBinaries(clientimplementation.CommandOptions{
		Ctx:     ctx,
		Name:    "listProjects",
		Command: provider.Exec.Proxy.List.Projects,
		Context: devsyConfig.DefaultContext,
		Options: devsyConfig.ProviderOptions(provider.Name),
		Config:  provider,
		Stdout:  &buf,
	})
	if err != nil {
		return fmt.Errorf("watch workspaces with provider \"%s\": %w", provider.Name, err)
	}

	fmt.Println(buf.String())

	return nil
}
