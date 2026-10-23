package cluster

import (
	"context"
	"encoding/json"
	"fmt"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	"github.com/devsy-org/devsy/cmd/pro/cmdutil"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/pkg/client/proxycmd"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/spf13/cobra"
)

// ListClustersCmd holds the cmd flags.
type ListClustersCmd struct {
	*flags.GlobalFlags

	Host    string
	Project string
}

// NewListCmd creates a new command.
func NewListCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListClustersCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "list",
		Short:  "List clusters",
		Hidden: true,
		RunE:   cmdutil.HostRunE(globalFlags, &cmd.Host, cmd.Run),
	}
	cmdutil.AddHostFlag(c, &cmd.Host)
	cliflags.Add(c, cliflags.String(&cmd.Project, names.Project, "", "The project to use"))
	_ = c.MarkFlagRequired(names.Project)
	flags.BindEnv(c.Flags(), names.Project)

	return c
}

func (cmd *ListClustersCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider.ProviderConfig,
) error {
	headers := []string{cmdutil.HeaderName, cmdutil.HeaderDisplayName, "Online"}

	return proxycmd.RunAndPrintTable(ctx, proxycmd.Options{
		Command:     provider.Exec.Proxy.List.Clusters,
		DevsyConfig: devsyConfig,
		Provider:    provider,
		ExtraOptions: map[string]config.OptionValue{
			platform.ProjectEnv: {Value: cmd.Project},
		},
	}, headers, func(payload []byte) ([][]string, error) {
		clusters := managementv1.ProjectClusters{}
		if err := json.Unmarshal(payload, &clusters); err != nil {
			return nil, err
		}

		rows := make([][]string, 0, len(clusters.Clusters))
		for _, c := range clusters.Clusters {
			rows = append(rows, []string{
				c.GetName(),
				c.Spec.DisplayName,
				fmt.Sprintf("%t", c.Status.Online),
			})
		}
		return rows, nil
	})
}
