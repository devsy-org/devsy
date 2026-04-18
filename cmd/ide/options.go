package ide

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/ide"
	"github.com/devsy-org/devsy/pkg/ide/ideparse"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/spf13/cobra"
)

// OptionsCmd holds the options cmd flags.
type OptionsCmd struct {
	*flags.GlobalFlags

	Output string
}

// NewOptionsCmd creates a new command.
func NewOptionsCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &OptionsCmd{
		GlobalFlags: flags,
	}
	optionsCmd := &cobra.Command{
		Use:   "options",
		Short: "List ide options",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("please specify the ide")
			}

			return cmd.Run(cobraCmd.Context(), args[0])
		},
	}

	optionsCmd.Flags().
		StringVar(&cmd.Output, "output", "plain", "The output format to use. Can be json or plain")
	return optionsCmd
}

type optionWithValue struct {
	ide.Option `json:",inline"`

	Value string `json:"value,omitempty"`
}

// Run runs the command logic.
func (cmd *OptionsCmd) Run(ctx context.Context, ide string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	values := devsyConfig.IDEOptions(ide)
	ideOptions, err := ideparse.GetIDEOptions(ide)
	if err != nil {
		return err
	}

	switch cmd.Output {
	case "plain":
		tableEntries := [][]string{}
		for optionName, entry := range ideOptions {
			value := values[optionName].Value
			tableEntries = append(tableEntries, []string{
				optionName,
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
	case "json":
		options := map[string]optionWithValue{}
		for optionName, entry := range ideOptions {
			options[optionName] = optionWithValue{
				Option: entry,
				Value:  values[optionName].Value,
			}
		}

		out, err := json.Marshal(options)
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
