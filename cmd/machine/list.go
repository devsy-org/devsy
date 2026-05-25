package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/output"
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/table"
	"github.com/spf13/cobra"
)

// ListCmd holds the configuration.
type ListCmd struct {
	*flags.GlobalFlags
}

// NewListCmd creates a new list command.
func NewListCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &ListCmd{
		GlobalFlags: flags,
	}
	listCmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "Lists existing machines",
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			return cmd.Run(cobraCmd.Context())
		},
	}

	return listCmd
}

// Run runs the command logic.
func (cmd *ListCmd) Run(ctx context.Context) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}

	machineDir, err := provider.GetMachinesDir(devsyConfig.DefaultContext)
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(machineDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	mode, err := output.ResolveMode(cmd.ResultFormat)
	if err != nil {
		return err
	}
	switch mode {
	case output.ModePlain:
		tableEntries := [][]string{}
		for _, entry := range entries {
			machineConfig, err := provider.LoadMachineConfig(
				devsyConfig.DefaultContext,
				entry.Name(),
			)
			if err != nil {
				return fmt.Errorf("load machine config: %w", err)
			}

			tableEntries = append(tableEntries, []string{
				machineConfig.ID,
				machineConfig.Provider.Name,
				time.Since(machineConfig.CreationTimestamp.Time).Round(1 * time.Second).String(),
			})
		}
		sort.SliceStable(tableEntries, func(i, j int) bool {
			return tableEntries[i][0] < tableEntries[j][0]
		})

		table.Print([]string{
			"Name",
			"Provider",
			"Age",
		}, tableEntries)
	case output.ModeJSON:
		tableEntries := []*provider.Machine{}
		for _, entry := range entries {
			machineConfig, err := provider.LoadMachineConfig(
				devsyConfig.DefaultContext,
				entry.Name(),
			)
			if err != nil {
				return fmt.Errorf("load machine config: %w", err)
			}

			tableEntries = append(tableEntries, machineConfig)
		}
		sort.SliceStable(tableEntries, func(i, j int) bool {
			return tableEntries[i].ID < tableEntries[j].ID
		})
		out, err := json.Marshal(tableEntries)
		if err != nil {
			return err
		}
		fmt.Print(string(out))
	}

	return nil
}
