package project

import (
	"context"
	"encoding/json"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/cmd/pro/proutil"
	"github.com/devsy-org/devsy/pkg/client/proxycmd"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// ListProjectsCmd holds the cmd flags.
type ListProjectsCmd struct {
	*flags.GlobalFlags

	Host string
}

// NewListCmd creates a new command.
func NewListCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListProjectsCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "list",
		Short:  "List projects",
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

	return c
}

func (cmd *ListProjectsCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider.ProviderConfig,
) error {
	headers := []string{proutil.HeaderName, proutil.HeaderDisplayName, "Description"}

	return proxycmd.RunAndPrintTable(ctx, proxycmd.Options{
		Command:     provider.Exec.Proxy.List.Projects,
		DevsyConfig: devsyConfig,
		Provider:    provider,
	}, headers, func(payload []byte) ([][]string, error) {
		projects := []managementv1.Project{}
		if err := json.Unmarshal(payload, &projects); err != nil {
			return nil, err
		}

		rows := make([][]string, 0, len(projects))
		for _, p := range projects {
			rows = append(rows, []string{
				p.GetName(),
				p.Spec.DisplayName,
				p.Spec.Description,
			})
		}
		return rows, nil
	})
}
