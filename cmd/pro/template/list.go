package template

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/cmd/pro/proutil"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
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

// NewListCmd creates a new command.
func NewListCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListTemplatesCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "list",
		Short:  "List templates",
		Hidden: true,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, provider, err := proutil.FindProProvider(
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

	cliflags.Add(c, cliflags.String(&cmd.Host, names.Host, "", "The pro instance to use"))
	_ = c.MarkFlagRequired(names.Host)
	flags.BindEnv(c.Flags(), names.Host)
	cliflags.Add(c, cliflags.String(&cmd.Project, names.Project, "", "The project to use"))
	_ = c.MarkFlagRequired(names.Project)
	flags.BindEnv(c.Flags(), names.Project)

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
		Command: provider.Exec.Proxy.List.Templates,
		Context: devsyConfig.DefaultContext,
		Options: opts,
		Config:  provider,
		Stdout:  &buf,
	})
	if err != nil {
		return fmt.Errorf("list templates with provider %q: %w", provider.Name, err)
	}

	headers := []string{proutil.HeaderName, proutil.HeaderDisplayName, "Description"}
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
