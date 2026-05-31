package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// ListCmd holds the configuration.
type ListCmd struct {
	*flags.GlobalFlags

	SkipPro bool
}

// NewListCmd creates a new destroy command.
func NewListCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListCmd{
		GlobalFlags: flags,
	}
	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Lists existing workspaces",
		Args:    cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	listCmd.Flags().BoolVar(&cmd.SkipPro, "skip-pro", false, "Don't list pro workspaces")
	return listCmd
}

// Run runs the command logic.
func (cmd *ListCmd) Run(ctx context.Context) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	workspaces, err := workspace.List(ctx, devsyConfig, cmd.SkipPro, cmd.Owner)
	if err != nil {
		return err
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	switch mode {
	case output.ModeJSON:
		sort.SliceStable(workspaces, func(i, j int) bool {
			return workspaces[i].LastUsedTimestamp.Unix() > workspaces[j].LastUsedTimestamp.Unix()
		})
		out, err := json.Marshal(workspaces)
		if err != nil {
			return err
		}
		fmt.Print(string(out))
	case output.ModePlain:
		tableEntries := [][]string{}
		sort.SliceStable(workspaces, func(i, j int) bool {
			return workspaces[i].LastUsedTimestamp.Unix() > workspaces[j].LastUsedTimestamp.Unix()
		})
		for _, entry := range workspaces {
			name := entry.ID
			if entry.IsPro() && entry.Pro.DisplayName != "" && entry.ID != entry.Pro.DisplayName {
				name = fmt.Sprintf("%s (%s)", entry.Pro.DisplayName, entry.ID)
			}
			tableEntries = append(tableEntries, []string{
				name,
				entry.Source.String(),
				entry.Machine.ID,
				entry.Provider.Name,
				entry.IDE.Name,
				time.Since(entry.LastUsedTimestamp.Time).Round(1 * time.Second).String(),
				time.Since(entry.CreationTimestamp.Time).Round(1 * time.Second).String(),
				fmt.Sprintf("%t", entry.IsPro()),
			})
		}

		table.Print([]string{
			"Name",
			"Source",
			"Machine",
			"Provider",
			"IDE",
			"Last Used",
			"Age",
			"Pro",
		}, tableEntries)
	}

	return nil
}
