package context

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/spf13/cobra"
)

// ListCmd holds the list cmd flags.
type ListCmd struct {
	*flags.GlobalFlags

	Output string
}

// NewListCmd creates a new command.
func NewListCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListCmd{
		GlobalFlags: flags,
	}
	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List Devsy contexts",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	listCmd.Flags().
		StringVar(&cmd.Output, "output", "plain", "The output format to use. Can be json or plain")
	return listCmd
}

type ContextWithDefault struct {
	Name string `json:"name,omitempty"`

	Default bool `json:"default,omitempty"`
}

// Run runs the command logic.
func (cmd *ListCmd) Run(ctx context.Context) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	switch cmd.Output {
	case "plain":
		tableEntries := [][]string{}
		for contextName := range devsyConfig.Contexts {
			tableEntries = append(tableEntries, []string{
				contextName,
				strconv.FormatBool(devsyConfig.DefaultContext == contextName),
			})
		}
		sort.SliceStable(tableEntries, func(i, j int) bool {
			return tableEntries[i][0] < tableEntries[j][0]
		})

		table.Print([]string{
			"Name",
			"Default",
		}, tableEntries)
	case "json":
		ides := []ContextWithDefault{}
		for contextName := range devsyConfig.Contexts {
			ides = append(ides, ContextWithDefault{
				Name:    contextName,
				Default: devsyConfig.DefaultContext == contextName,
			})
		}

		out, err := json.MarshalIndent(ides, "", "  ")
		if err != nil {
			return err
		}
		fmt.Print(string(out))
	default:
		return fmt.Errorf(
			"unexpected output format, choose either json or plain. Got %s",
			cmd.Output,
		)
	}

	return nil
}
