package context

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/spf13/cobra"
)

// OptionsCmd holds the options cmd flags.
type OptionsCmd struct {
	*flags.GlobalFlags
}

// NewOptionsCmd creates a new command.
func NewOptionsCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &OptionsCmd{
		GlobalFlags: flags,
	}
	optionsCmd := &cobra.Command{
		Use:   "get",
		Short: "Show options of a context",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
	}

	return optionsCmd
}

type optionWithValue struct {
	config.ContextOption `json:",inline"`

	Value string `json:"value,omitempty"`
}

// Run runs the command logic.
func (cmd *OptionsCmd) Run(ctx context.Context, args []string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, "")
	if err != nil {
		return err
	}

	entryOptions := devsyConfig.Current().Options
	if entryOptions == nil {
		entryOptions = map[string]config.OptionValue{}
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	switch mode {
	case output.ModePlain:
		tableEntries := [][]string{}
		for _, entry := range config.ContextOptions {
			value := entryOptions[entry.Name].Value

			tableEntries = append(tableEntries, []string{
				entry.Name,
				entry.Description,
				entry.Default,
				value,
			})
		}
		sort.SliceStable(tableEntries, func(i, j int) bool {
			return tableEntries[i][0] < tableEntries[j][0]
		})

		table.Print([]string{
			"Name",
			"Description",
			"Default",
			"Value",
		}, tableEntries)
	case output.ModeJSON:
		options := map[string]optionWithValue{}
		for _, entry := range config.ContextOptions {
			options[entry.Name] = optionWithValue{
				ContextOption: entry,
				Value:         entryOptions[entry.Name].Value,
			}
		}

		out, err := json.MarshalIndent(options, "", "  ")
		if err != nil {
			return err
		}
		fmt.Print(string(out))
	}

	return nil
}
