package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/extract"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	snapshotpkg "github.com/devsy-org/devsy/pkg/snapshot"
	"github.com/devsy-org/devsy/pkg/workspace"
	"github.com/spf13/cobra"
)

// ImportCmd holds the export cmd flags.
type ImportCmd struct {
	*flags.GlobalFlags

	WorkspaceID string

	MachineID    string
	MachineReuse bool

	ProviderID    string
	ProviderReuse bool

	Data string
}

// NewImportCmd creates a new command.
func NewImportCmd(globalFlags *flags.GlobalFlags) *cobra.Command {
	cmd := &ImportCmd{
		GlobalFlags: globalFlags,
	}
	importCmd := &cobra.Command{
		Use:    "import",
		Short:  "Import workspace configuration",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cobraCmd *cobra.Command, _ []string) error {
			return cmd.execute(cobraCmd.Context())
		},
	}

	cliflags.Add(importCmd,
		cliflags.String(&cmd.WorkspaceID, names.WorkspaceID, "", "To workspace id to use"),
		cliflags.String(&cmd.MachineID, names.MachineID, "", "The machine id to use"),
		cliflags.Bool(&cmd.MachineReuse, names.MachineReuse, false,
			"If machine already exists, reuse existing machine"),
		cliflags.String(&cmd.ProviderID, names.ProviderID, "", "The provider id to use"),
		cliflags.Bool(&cmd.ProviderReuse, names.ProviderReuse, false,
			"If provider already exists, reuse existing provider"),
		cliflags.String(&cmd.Data, names.Data, "", "The data to import as raw json"),
	)
	_ = importCmd.MarkFlagRequired(names.Data)
	return importCmd
}

func (cmd *ImportCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
) error {
	exportConfig, err := cmd.parseExportConfig()
	if err != nil {
		return err
	}

	cmd.setDefaultIDs(exportConfig)

	if err := cmd.checkForConflictingIDs(ctx, exportConfig, devsyConfig); err != nil {
		return err
	}

	if err := cmd.importProvider(devsyConfig, exportConfig); err != nil {
		return err
	}

	if err := cmd.importMachine(devsyConfig, exportConfig); err != nil {
		return err
	}

	return cmd.importWorkspace(devsyConfig, exportConfig)
}

func (cmd *ImportCmd) execute(ctx context.Context) error {
	devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
	if err != nil {
		return err
	}
	return cmd.Run(ctx, devsyConfig)
}

func (cmd *ImportCmd) parseExportConfig() (*provider.ExportConfig, error) {
	exportConfig := &provider.ExportConfig{}
	if err := json.Unmarshal([]byte(cmd.Data), exportConfig); err != nil {
		return nil, fmt.Errorf("decode workspace data: %w", err)
	}
	if exportConfig.Workspace == nil {
		return nil, fmt.Errorf("workspace is missing in imported data")
	}
	if exportConfig.Provider == nil {
		return nil, fmt.Errorf("provider is missing in imported data")
	}
	return exportConfig, nil
}

func (cmd *ImportCmd) setDefaultIDs(exportConfig *provider.ExportConfig) {
	if cmd.MachineID == "" && exportConfig.Machine != nil {
		cmd.MachineID = exportConfig.Machine.ID
	}
	if cmd.WorkspaceID == "" {
		cmd.WorkspaceID = exportConfig.Workspace.ID
	}
	if cmd.ProviderID == "" {
		cmd.ProviderID = exportConfig.Provider.ID
	}
}

func (cmd *ImportCmd) importWorkspace(
	devsyConfig *config.Config,
	exportConfig *provider.ExportConfig,
) error {
	workspaceDir, err := provider.GetWorkspaceDir(devsyConfig.DefaultContext, cmd.WorkspaceID)
	if err != nil {
		return fmt.Errorf("get workspace dir: %w", err)
	}

	// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
	err = os.MkdirAll(workspaceDir, 0o755)
	if err != nil {
		return fmt.Errorf("create workspace dir: %w", err)
	}

	decoded, err := base64.RawStdEncoding.DecodeString(exportConfig.Workspace.Data)
	if err != nil {
		return fmt.Errorf("decode workspace data: %w", err)
	}

	err = extract.Extract(bytes.NewReader(decoded), workspaceDir)
	if err != nil {
		return fmt.Errorf("extract workspace data: %w", err)
	}

	// exchange config
	workspaceConfig, err := provider.LoadWorkspaceConfig(
		devsyConfig.DefaultContext,
		cmd.WorkspaceID,
	)
	if err != nil {
		return fmt.Errorf("load machine config: %w", err)
	}
	workspaceConfig.ID = cmd.WorkspaceID
	workspaceConfig.Context = devsyConfig.DefaultContext
	workspaceConfig.Machine.ID = cmd.MachineID
	workspaceConfig.Provider.Name = cmd.ProviderID

	if exportConfig.SnapshotRef != "" {
		sourceStr, devContainerSource, err := snapshotpkg.RestoreComposition(
			exportConfig.SnapshotRef,
		)
		if err != nil {
			return fmt.Errorf("parse snapshot ref: %w", err)
		}
		parsedSource := provider.ParseWorkspaceSource(sourceStr)
		if parsedSource == nil {
			return fmt.Errorf(
				"compose workspace source from snapshot ref: unexpected source %q",
				sourceStr,
			)
		}
		workspaceConfig.Source = *parsedSource
		workspaceConfig.DevContainerSource = devContainerSource
	}

	// save machine config
	err = provider.SaveWorkspaceConfig(workspaceConfig)
	if err != nil {
		return fmt.Errorf("save workspace config: %w", err)
	}

	log.Infof("imported workspace: workspaceId=%s", cmd.WorkspaceID)
	return nil
}

func (cmd *ImportCmd) importMachine(
	devsyConfig *config.Config,
	exportConfig *provider.ExportConfig,
) error {
	if exportConfig.Machine == nil {
		return nil
	}

	// if machine already exists we skip
	if cmd.MachineReuse && provider.MachineExists(devsyConfig.DefaultContext, cmd.MachineID) {
		log.Infof("Reusing existing machine %s", cmd.MachineID)
		return nil
	}

	machineDir, err := provider.GetMachineDir(devsyConfig.DefaultContext, cmd.MachineID)
	if err != nil {
		return fmt.Errorf("get machine dir: %w", err)
	}

	if err := extractExportDir(machineDir, exportConfig.Machine.Data, "machine"); err != nil {
		return err
	}

	// exchange config
	machineConfig, err := provider.LoadMachineConfig(devsyConfig.DefaultContext, cmd.MachineID)
	if err != nil {
		return fmt.Errorf("load machine config: %w", err)
	}
	machineConfig.ID = cmd.MachineID
	machineConfig.Context = devsyConfig.DefaultContext
	machineConfig.Provider.Name = cmd.ProviderID

	// save machine config
	err = provider.SaveMachineConfig(machineConfig)
	if err != nil {
		return fmt.Errorf("save machine config: %w", err)
	}

	log.Infof("imported machine: machineId=%s", cmd.MachineID)
	return nil
}

func (cmd *ImportCmd) importProvider(
	devsyConfig *config.Config,
	exportConfig *provider.ExportConfig,
) error {
	// if provider already exists we skip
	if cmd.ProviderReuse && provider.ProviderExists(devsyConfig.DefaultContext, cmd.ProviderID) {
		log.Infof("Reusing existing provider %s", cmd.ProviderID)
		return nil
	}

	providerDir, err := provider.GetProviderDir(devsyConfig.DefaultContext, cmd.ProviderID)
	if err != nil {
		return fmt.Errorf("get provider dir: %w", err)
	}

	if err := extractExportDir(providerDir, exportConfig.Provider.Data, "provider"); err != nil {
		return err
	}

	// exchange config
	providerConfig, err := provider.LoadProviderConfig(devsyConfig.DefaultContext, cmd.ProviderID)
	if err != nil {
		return fmt.Errorf("load provider config: %w", err)
	}
	providerConfig.Name = cmd.ProviderID

	// save provider config
	err = provider.SaveProviderConfig(devsyConfig.DefaultContext, providerConfig)
	if err != nil {
		return fmt.Errorf("save provider config: %w", err)
	}

	if err := cmd.applyProviderOptions(devsyConfig, exportConfig); err != nil {
		return err
	}

	log.Infof("imported provider: providerId=%s", cmd.ProviderID)
	return nil
}

func (cmd *ImportCmd) applyProviderOptions(
	devsyConfig *config.Config,
	exportConfig *provider.ExportConfig,
) error {
	if exportConfig.Provider.Config == nil {
		return nil
	}
	if devsyConfig.Current().Providers == nil {
		devsyConfig.Current().Providers = map[string]*config.ProviderConfig{}
	}

	devsyConfig.Current().Providers[cmd.ProviderID] = exportConfig.Provider.Config
	if err := config.SaveConfig(devsyConfig); err != nil {
		return fmt.Errorf("save devsy config: %w", err)
	}
	return nil
}

func extractExportDir(dir, data, label string) error {
	// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s dir: %w", label, err)
	}

	decoded, err := base64.RawStdEncoding.DecodeString(data)
	if err != nil {
		return fmt.Errorf("decode %s data: %w", label, err)
	}

	if err := extract.Extract(bytes.NewReader(decoded), dir); err != nil {
		return fmt.Errorf("extract %s data: %w", label, err)
	}
	return nil
}

func (cmd *ImportCmd) checkForConflictingIDs(
	ctx context.Context,
	exportConfig *provider.ExportConfig,
	devsyConfig *config.Config,
) error {
	workspaces, err := workspace.List(ctx, devsyConfig, false, cmd.Owner)
	if err != nil {
		return fmt.Errorf("error listing workspaces: %w", err)
	}

	if err := cmd.checkWorkspaceConflict(exportConfig, workspaces); err != nil {
		return err
	}
	if err := cmd.checkMachineConflict(exportConfig, devsyConfig); err != nil {
		return err
	}
	return cmd.checkProviderConflict(exportConfig, devsyConfig)
}

func (cmd *ImportCmd) checkWorkspaceConflict(
	exportConfig *provider.ExportConfig,
	workspaces []*provider.Workspace,
) error {
	if exportConfig.Workspace == nil {
		return nil
	}
	for _, workspace := range workspaces {
		if workspace.ID == cmd.WorkspaceID {
			return fmt.Errorf(
				"existing workspace with id %s found, use --workspace-id to override the workspace id",
				cmd.WorkspaceID,
			)
		} else if workspace.UID == exportConfig.Workspace.UID {
			return fmt.Errorf(
				"existing workspace %s with uid %s found, use --workspace-id to override the workspace id",
				workspace.ID,
				workspace.UID,
			)
		}
	}
	return nil
}

func (cmd *ImportCmd) checkMachineConflict(
	exportConfig *provider.ExportConfig,
	devsyConfig *config.Config,
) error {
	if !cmd.MachineReuse && exportConfig.Machine != nil {
		if provider.MachineExists(devsyConfig.DefaultContext, cmd.MachineID) {
			return fmt.Errorf(
				"existing machine with id %s found, use --machine-reuse to skip importing "+
					"the machine or --machine-id to override the machine id",
				cmd.MachineID,
			)
		}
	}
	return nil
}

func (cmd *ImportCmd) checkProviderConflict(
	exportConfig *provider.ExportConfig,
	devsyConfig *config.Config,
) error {
	if !cmd.ProviderReuse && exportConfig.Provider != nil {
		if provider.ProviderExists(devsyConfig.DefaultContext, cmd.ProviderID) {
			return fmt.Errorf(
				"existing provider with id %s found, use --provider-reuse to skip importing "+
					"the provider or --provider-id to override the provider id",
				cmd.ProviderID,
			)
		}
	}
	return nil
}
