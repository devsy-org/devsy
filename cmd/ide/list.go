package ide

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide/ideparse"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/spf13/cobra"
)

// ListCmd holds the list cmd flags.
type ListCmd struct {
	*flags.GlobalFlags
}

// NewListCmd creates a new command.
func NewListCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListCmd{
		GlobalFlags: flags,
	}
	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List available IDEs",
		Args:    cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	return listCmd
}

type IDEWithDefault struct {
	ideparse.AllowedIDE `json:",inline"`

	Default bool `json:"default,omitempty"`
}

// Run runs the command logic.
func (cmd *ListCmd) Run(ctx context.Context) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	switch mode {
	case output.ModePlain:
		tableEntries := [][]string{}
		for _, entry := range ideparse.AllowedIDEs {
			marker := ""
			if devsyConfig.Current().DefaultIDE == string(entry.Name) {
				marker = "*"
			}
			tableEntries = append(tableEntries, []string{
				string(entry.Name),
				marker,
			})
		}
		sort.SliceStable(tableEntries, func(i, j int) bool {
			return tableEntries[i][0] < tableEntries[j][0]
		})

		table.Print([]string{
			"Name",
			"Default",
		}, tableEntries)
	case output.ModeJSON:
		ides := []IDEWithDefault{}
		for _, entry := range ideparse.AllowedIDEs {
			ides = append(ides, IDEWithDefault{
				AllowedIDE: entry,
				Default:    devsyConfig.Current().DefaultIDE == string(entry.Name),
			})
		}

		out, err := json.MarshalIndent(ides, "", "  ")
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintln(os.Stdout, string(out))
	}

	return nil
}
