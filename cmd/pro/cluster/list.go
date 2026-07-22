package cluster

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

func (cmd *ListClustersCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	provider *provider.ProviderConfig,
) error {
	opts := devsyConfig.ProviderOptions(provider.Name)
	opts[platform.ProjectEnv] = config.OptionValue{Value: cmd.Project}

	var buf bytes.Buffer
	err := clientimplementation.RunCommandWithBinaries(clientimplementation.CommandOptions{
		Ctx:     ctx,
		Name:    "listClusters",
		Command: provider.Exec.Proxy.List.Clusters,
		Context: devsyConfig.DefaultContext,
		Options: opts,
		Config:  provider,
		Stdout:  &buf,
	})
	if err != nil {
		return fmt.Errorf("list clusters with provider %q: %w", provider.Name, err)
	}

	headers := []string{proutil.HeaderName, proutil.HeaderDisplayName, "Online"}
	if buf.Len() == 0 {
		table.Print(headers, nil)
		return nil
	}

	clusters := managementv1.ProjectClusters{}
	if err := json.Unmarshal(buf.Bytes(), &clusters); err != nil {
		return fmt.Errorf("parse clusters output: %w", err)
	}

	rows := make([][]string, 0, len(clusters.Clusters))
	for _, c := range clusters.Clusters {
		rows = append(rows, []string{
			c.GetName(),
			c.Spec.DisplayName,
			fmt.Sprintf("%t", c.Status.Online),
		})
	}
	table.Print(headers, rows)

	return nil
}
