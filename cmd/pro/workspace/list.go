package workspace

import (
	"context"
	"encoding/json"
	"time"

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

// ListWorkspacesCmd holds the cmd flags.
type ListWorkspacesCmd struct {
	*flags.GlobalFlags

	Host string
}

// NewListCmd creates a new command.
func NewListCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListWorkspacesCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "list",
		Short:  "List Workspaces",
		Hidden: true,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, providerConfig, err := proutil.FindProProvider(
				cobraCmd.Context(),
				cmd.Context,
				cmd.Provider,
				cmd.Host,
			)
			if err != nil {
				return err
			}

			return cmd.Run(cobraCmd.Context(), devsyConfig, providerConfig)
		},
	}

	cliflags.Add(c, cliflags.String(&cmd.Host, names.Host, "", "The pro instance to use"))
	_ = c.MarkFlagRequired(names.Host)
	flags.BindEnv(c.Flags(), names.Host)

	return c
}

func (cmd *ListWorkspacesCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider.ProviderConfig,
) error {
	headers := []string{proutil.HeaderName, proutil.HeaderDisplayName, "Project", "Age"}

	return proxycmd.RunAndPrintTable(ctx, proxycmd.Options{
		Command:     provider.Exec.Proxy.List.Workspaces,
		DevsyConfig: devsyConfig,
		Provider:    provider,
	}, headers, func(payload []byte) ([][]string, error) {
		instances := []managementv1.DevsyWorkspaceInstance{}
		if err := json.Unmarshal(payload, &instances); err != nil {
			return nil, err
		}

		rows := make([][]string, 0, len(instances))
		for _, inst := range instances {
			project := ""
			if inst.GetLabels() != nil {
				project = inst.GetLabels()["devsy.sh/project"]
			}
			age := ""
			if !inst.CreationTimestamp.IsZero() {
				age = time.Since(inst.CreationTimestamp.Time).Round(time.Second).String()
			}
			rows = append(rows, []string{
				inst.GetName(),
				inst.Spec.DisplayName,
				project,
				age,
			})
		}
		return rows, nil
	})
}
