package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"sort"
	"strconv"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// OptionsCmd holds the options cmd flags.
type OptionsCmd struct {
	*flags.GlobalFlags

	Hidden bool
}

// NewOptionsCmd creates a new command.
func NewOptionsCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &OptionsCmd{
		GlobalFlags: flags,
	}
	optionsCmd := &cobra.Command{
		Use:   "options [provider]",
		Short: "Show options of an existing provider",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context(), args)
		},
		ValidArgsFunction: func(rootCmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return completion.GetProviderSuggestions(
				rootCmd,
				cmd.Context,
				cmd.Provider,
				args,
				toComplete,
				cmd.Owner,
			)
		},
	}

	optionsCmd.Flags().
		BoolVar(&cmd.Hidden, "hidden", false, "If true, will also show hidden options.")
	return optionsCmd
}

type optionWithValue struct {
	types.Option `json:",inline"`

	Children []string `json:"children,omitempty"`
	Value    string   `json:"value,omitempty"`
}

// Run runs the command logic.
func (cmd *OptionsCmd) Run(ctx context.Context, args []string) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	providerName := devsyConfig.Current().DefaultProvider
	if len(args) > 0 {
		providerName = args[0]
	} else if providerName == "" {
		return fmt.Errorf("please specify a provider")
	}

	if providerName != "" && cmd.Provider != "" {
		if providerName != cmd.Provider {
			log.Infof("providerName=%+v", providerName)
			log.Infof("GlobalFlags.Provider=%+v", cmd.Provider)
			return fmt.Errorf("ambiguous provider configuration detected")
		}
	}

	providerWithOptions, err := workspace.FindProvider(
		devsyConfig,
		providerName,
	)
	if err != nil {
		return err
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	return printOptions(devsyConfig, providerWithOptions, mode, cmd.Hidden)
}

func printOptions(
	devsyConfig *config.Config,
	provider *workspace.ProviderWithOptions,
	format string,
	showHidden bool,
) error {
	entryOptions := devsyConfig.ProviderOptions(provider.Config.Name)
	dynamicOptions := devsyConfig.DynamicProviderOptionDefinitions(provider.Config.Name)
	srcOptions := MergeDynamicOptions(provider.Config.Options, dynamicOptions)
	switch format {
	case output.ModePlain:
		printOptionsPlain(srcOptions, entryOptions, showHidden)
	case output.ModeJSON:
		return printOptionsJSON(srcOptions, entryOptions, showHidden)
	}

	return nil
}

func printOptionsPlain(
	srcOptions map[string]*types.Option,
	entryOptions map[string]config.OptionValue,
	showHidden bool,
) {
	tableEntries := [][]string{}
	for optionName, entry := range srcOptions {
		if !showHidden && entry.Hidden {
			continue
		}

		value := entryOptions[optionName].Value
		if value != "" && entry.Password {
			value = "********"
		}

		tableEntries = append(tableEntries, []string{
			optionName,
			strconv.FormatBool(entry.Required),
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
		"Required",
		"Description",
		"Default",
		"Value",
	}, tableEntries)
}

func printOptionsJSON(
	srcOptions map[string]*types.Option,
	entryOptions map[string]config.OptionValue,
	showHidden bool,
) error {
	options := map[string]optionWithValue{}
	for optionName, entry := range srcOptions {
		if !showHidden && entry.Hidden {
			continue
		}

		options[optionName] = optionWithValue{
			Option:   *entry,
			Children: entryOptions[optionName].Children,
			Value:    entryOptions[optionName].Value,
		}
	}

	out, err := json.MarshalIndent(options, "", "  ")
	if err != nil {
		return err
	}
	_, _ = os.Stdout.Write(out)
	return nil
}

// MergeDynamicOptions merges the static provider options and dynamic options.
func MergeDynamicOptions(
	options map[string]*types.Option,
	dynamicOptions config.OptionDefinitions,
) map[string]*types.Option {
	retOptions := map[string]*types.Option{}
	maps.Copy(retOptions, options)
	maps.Copy(retOptions, dynamicOptions)

	return retOptions
}
