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
		return printMachinesPlain(devsyConfig, entries)
	case output.ModeJSON:
		return printMachinesJSON(devsyConfig, entries)
	}

	return nil
}

func loadSortedMachines(
	devsyConfig *config.Config,
	entries []os.DirEntry,
) ([]*provider.Machine, error) {
	machines := make([]*provider.Machine, 0, len(entries))
	for _, entry := range entries {
		machineConfig, err := provider.LoadMachineConfig(
			devsyConfig.DefaultContext,
			entry.Name(),
		)
		if err != nil {
			return nil, fmt.Errorf("load machine config: %w", err)
		}

		machines = append(machines, machineConfig)
	}
	sort.SliceStable(machines, func(i, j int) bool {
		return machines[i].ID < machines[j].ID
	})
	return machines, nil
}

func printMachinesPlain(devsyConfig *config.Config, entries []os.DirEntry) error {
	machines, err := loadSortedMachines(devsyConfig, entries)
	if err != nil {
		return err
	}

	tableEntries := [][]string{}
	for _, machineConfig := range machines {
		tableEntries = append(tableEntries, []string{
			machineConfig.ID,
			machineConfig.Provider.Name,
			time.Since(machineConfig.CreationTimestamp.Time).Round(1 * time.Second).String(),
		})
	}

	table.Print([]string{
		"Name",
		"Provider",
		"Age",
	}, tableEntries)
	return nil
}

func printMachinesJSON(devsyConfig *config.Config, entries []os.DirEntry) error {
	machines, err := loadSortedMachines(devsyConfig, entries)
	if err != nil {
		return err
	}

	out, err := json.Marshal(machines)
	if err != nil {
		return err
	}
	fmt.Print(string(out)) //nolint:forbidigo // CLI stdout output
	return nil
}
