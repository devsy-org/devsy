package pro

import (
	"bytes"
	"context"
	"fmt"

	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// ListTemplatesCmd holds the cmd flags.
type ListTemplatesCmd struct {
	*flags.GlobalFlags

	Host    string
	Project string
}

// NewListTemplatesCmd creates a new command.
func NewListTemplatesCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListTemplatesCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "list-templates",
		Short:  "List templates",
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
	c.Flags().StringVar(&cmd.Project, "project", "", "The project to use")
	_ = c.MarkFlagRequired("project")

	return c
}

func (cmd *ListTemplatesCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider.ProviderConfig,
) error {
	opts := devsyConfig.ProviderOptions(provider.Name)
	opts[platform.ProjectEnv] = config.OptionValue{Value: cmd.Project}

	var buf bytes.Buffer
	err := clientimplementation.RunCommandWithBinaries(clientimplementation.CommandOptions{
		Ctx:     ctx,
		Name:    "listTemplates",
		Command: provider.Exec.Proxy.List.Templates,
		Context: devsyConfig.DefaultContext,
		Options: opts,
		Config:  provider,
		Stdout:  &buf,
	})
	if err != nil {
		return fmt.Errorf("list templates with provider \"%s\": %w", provider.Name, err)
	}

	fmt.Println(buf.String())

	return nil
}
