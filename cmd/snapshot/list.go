package snapshot

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/output"
	snapshotpkg "github.com/devsy-org/devsy/pkg/snapshot"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

type ListCmd struct {
	*flags.GlobalFlags

	Registry string
}

func NewListCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListCmd{GlobalFlags: globalFlags}
	listCmd := &cobra.Command{
		Use:   "list [flags] [workspace-path|workspace-name]",
		Short: "List snapshots for a workspace",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}
			return cmd.Run(cobraCmd.Context(), devsyConfig, args)
		},
	}
	cliflags.Add(
		listCmd,
		cliflags.String(
			&cmd.Registry,
			"registry",
			"",
			"Registry to list snapshots from, overriding SNAPSHOT_REGISTRY",
		),
	)
	return listCmd
}

func (cmd *ListCmd) Run(ctx context.Context, devsyConfig *config.Config, args []string) error {
	wsClient, err := workspace.Get(ctx, workspace.GetOptions{
		DevsyConfig: devsyConfig,
		Args:        args,
		Owner:       cmd.Owner,
	})
	if err != nil {
		return err
	}

	workspaceConfig := wsClient.WorkspaceConfig()

	registry, err := resolveRegistry(cmd.Registry, devsyConfig)
	if err != nil {
		return err
	}

	refs, err := snapshotpkg.ListRefs(ctx, registry, workspaceConfig.ID)
	if err != nil {
		return err
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	switch mode {
	case output.ModeJSON:
		if err := printJSONRefs(refs); err != nil {
			return err
		}
	case output.ModePlain:
		printPlainRefs(refs)
	}

	return nil
}

func printJSONRefs(refs []*snapshotpkg.Ref) error {
	out, err := json.Marshal(refs)
	if err != nil {
		return err
	}
	fmt.Print(string(out)) //nolint:forbidigo // CLI stdout output
	return nil
}

func printPlainRefs(refs []*snapshotpkg.Ref) {
	if len(refs) == 0 {
		return
	}
	tableEntries := [][]string{}
	for _, ref := range refs {
		tableEntries = append(tableEntries, []string{
			ref.Tag,
			ref.Timestamp.Format("2006-01-02 15:04:05"),
		})
	}

	table.Print([]string{
		"Tag",
		"Created",
	}, tableEntries)
}
