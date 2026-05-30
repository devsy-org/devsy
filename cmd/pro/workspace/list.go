package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	managementv1 "github.com/devsy-org/api/pkg/apis/management/v1"
	"github.com/devsy-org/devsy/cmd/pro/flags"
	"github.com/devsy-org/devsy/cmd/pro/proutil"
	"github.com/devsy-org/devsy/pkg/client/clientimplementation"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/table"
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

	c.Flags().StringVar(&cmd.Host, "host", "", "The pro instance to use")
	_ = c.MarkFlagRequired("host")
	flags.BindEnv(c.Flags(), "host")

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

	headers := []string{proutil.HeaderName, proutil.HeaderDisplayName, "Project", "Age"}
	if buf.Len() == 0 {
		table.Print(headers, nil)
		return nil
	}

	instances := []managementv1.DevsyWorkspaceInstance{}
	if err := json.Unmarshal(buf.Bytes(), &instances); err != nil {
		return fmt.Errorf("parse workspaces output: %w", err)
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
	table.Print(headers, rows)

	return nil
}
