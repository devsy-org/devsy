package pro

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/table"
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
	flags.BindEnv(c.Flags(), "host")
	c.Flags().StringVar(&cmd.Project, "project", "", "The project to use")
	_ = c.MarkFlagRequired("project")
	flags.BindEnv(c.Flags(), "project")

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

	headers := []string{headerName, headerDisplayName, "Description"}
	if buf.Len() == 0 {
		table.Print(headers, nil)
		return nil
	}

	templates := managementv1.ProjectTemplates{}
	if err := json.Unmarshal(buf.Bytes(), &templates); err != nil {
		return fmt.Errorf("parse templates output: %w", err)
	}

	rows := make([][]string, 0, len(templates.DevsyWorkspaceTemplates))
	for _, t := range templates.DevsyWorkspaceTemplates {
		rows = append(rows, []string{
			t.GetName(),
			t.Spec.DisplayName,
			t.Spec.Description,
		})
	}
	table.Print(headers, rows)

	return nil
}
