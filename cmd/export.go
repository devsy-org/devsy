package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/devsy-org/devsy/cmd/completion"
	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/provider"
	workspace2 "github.com/devsy-org/devsy/pkg/workspace"
	"github.com/skevetter/log"
	"github.com/spf13/cobra"
)

// ExportCmd holds the export cmd flags.
type ExportCmd struct {
	*flags.GlobalFlags
}

// NewExportCmd creates a new command.
func NewExportCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &ExportCmd{
		GlobalFlags: flags,
	}
	exportCmd := &cobra.Command{
		Use:    "export [flags] [workspace-path|workspace-name]",
		Short:  "Exports a workspace configuration",
		Hidden: true,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			ctx := cobraCmd.Context()
			devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}

			return cmd.Run(ctx, devsyConfig, args)
		},
		ValidArgsFunction: func(
			rootCmd *cobra.Command, args []string, toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return completion.GetWorkspaceSuggestions(
				rootCmd,
				cmd.Context,
				cmd.Provider,
				args,
				toComplete,
				cmd.Owner,
				log.Default,
			)
		},
	}

	return exportCmd
}

// Run runs the command logic.
func (cmd *ExportCmd) Run(ctx context.Context, devsyConfig *config.Config, args []string) error {
	// try to load workspace
	logger := log.Default.ErrorStreamOnly()
	client, err := workspace2.Get(ctx, workspace2.GetOptions{
		DevsyConfig: devsyConfig,
		Args:         args,
		Owner:        cmd.Owner,
		Log:          logger,
	})
	if err != nil {
		return err
	}

	// export workspace
	exportConfig, err := exportWorkspace(devsyConfig, client.WorkspaceConfig())
	if err != nil {
		return err
	}

	// marshal config
	out, err := json.Marshal(exportConfig)
	if err != nil {
		return err
	}

	fmt.Println(string(out))
	return nil
}

func exportWorkspace(
	devsyConfig *config.Config,
	workspaceConfig *provider.Workspace,
) (*provider.ExportConfig, error) {
	var err error

	// create return config
	retConfig := &provider.ExportConfig{}

	// export workspace
	retConfig.Workspace, err = provider.ExportWorkspace(workspaceConfig.Context, workspaceConfig.ID)
	if err != nil {
		return nil, fmt.Errorf("export workspace config: %w", err)
	}

	// has machine?
	if workspaceConfig.Machine.ID != "" {
		retConfig.Machine, err = provider.ExportMachine(
			workspaceConfig.Context,
			workspaceConfig.Machine.ID,
		)
		if err != nil {
			return nil, fmt.Errorf("export machine config: %w", err)
		}
	}

	// export provider
	retConfig.Provider, err = provider.ExportProvider(
		devsyConfig,
		workspaceConfig.Context,
		workspaceConfig.Provider.Name,
	)
	if err != nil {
		return nil, fmt.Errorf("export provider config: %w", err)
	}

	return retConfig, nil
}
