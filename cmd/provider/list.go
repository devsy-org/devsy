package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/devsy-org/devsy/pkg/types"
	"github.com/devsy-org/devsy/pkg/workspace"
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
		Short:   "List available providers",
		Args:    cobra.NoArgs,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	return listCmd
}

type ProviderWithDefault struct {
	workspace.ProviderWithOptions `json:",inline"`

	Default bool `json:"default,omitempty"`
}

// Run runs the command logic.
func (cmd *ListCmd) Run(ctx context.Context) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	providers, err := workspace.LoadAllProviders(devsyConfig)
	if err != nil {
		return err
	}

	configuredProviders := devsyConfig.Current().Providers
	if configuredProviders == nil {
		configuredProviders = map[string]*config.ProviderConfig{}
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	switch mode {
	case output.ModePlain:
		tableEntries := [][]string{}
		for _, entry := range providers {
			tableEntries = append(tableEntries, []string{
				entry.Config.Name,
				entry.Config.Version,
				strconv.FormatBool(devsyConfig.Current().DefaultProvider == entry.Config.Name),
				strconv.FormatBool(entry.State != nil && entry.State.Initialized),
				entry.Config.Description,
			})
		}
		sort.SliceStable(tableEntries, func(i, j int) bool {
			return tableEntries[i][0] < tableEntries[j][0]
		})

		table.Print([]string{
			"Name",
			"Version",
			"Default",
			"Initialized",
			"Description",
		}, tableEntries)
	case output.ModeJSON:
		retMap := map[string]ProviderWithDefault{}
		for k, entry := range providers {
			var dynamicOptions map[string]*types.Option
			if configuredProviders[entry.Config.Name] != nil {
				dynamicOptions = configuredProviders[entry.Config.Name].DynamicOptions
			}

			srcOptions := MergeDynamicOptions(entry.Config.Options, dynamicOptions)
			entry.Config.Options = srcOptions
			retMap[k] = ProviderWithDefault{
				ProviderWithOptions: *entry,
				Default:             devsyConfig.Current().DefaultProvider == entry.Config.Name,
			}
		}

		out, err := json.MarshalIndent(retMap, "", "  ")
		if err != nil {
			return err
		}
		fmt.Print(string(out))
	}

	return nil
}
