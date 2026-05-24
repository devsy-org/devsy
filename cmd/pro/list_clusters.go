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

// ListClustersCmd holds the cmd flags.
type ListClustersCmd struct {
	*flags.GlobalFlags

	Host    string
	Project string
}

// NewListClustersCmd creates a new command.
func NewListClustersCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListClustersCmd{
		GlobalFlags: globalFlags,
	}
	c := &cobra.Command{
		Use:    "list-clusters",
		Short:  "List clusters",
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
		return fmt.Errorf("list clusters with provider \"%s\": %w", provider.Name, err)
	}

	if buf.Len() == 0 {
		table.Print([]string{headerName, "Online"}, nil)
		return nil
	}

	clusters := managementv1.ProjectClusters{}
	if err := json.Unmarshal(buf.Bytes(), &clusters); err != nil {
		return fmt.Errorf("parse clusters output: %w", err)
	}

	headers := []string{headerName, headerDisplayName, "Online"}
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
