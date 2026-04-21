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

// ListWorkspacesCmd holds the cmd flags.
type ListWorkspacesCmd struct {
	*flags.GlobalFlags

	Host string
}

// NewListWorkspacesCmd creates a new command.
func NewListWorkspacesCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListWorkspacesCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "list-workspaces",
		Short:  "List Workspaces",
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

	return c
}

func (cmd *ListWorkspacesCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider.ProviderConfig,
) error {
	var buf bytes.Buffer

	err := clientimplementation.RunCommandWithBinaries(clientimplementation.CommandOptions{
		Ctx:     ctx,
		Name:    "listWorkspaces",
		Command: provider.Exec.Proxy.List.Workspaces,
		Context: devsyConfig.DefaultContext,
		Options: devsyConfig.ProviderOptions(provider.Name),
		Config:  provider,
		Stdout:  &buf,
	})
	if err != nil {
		return fmt.Errorf("list workspaces: %w", err)
	}

	fmt.Println(buf.String())

	return nil
}
