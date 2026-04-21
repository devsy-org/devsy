package cmd

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
	"github.com/devsy-org/devsy/pkg/provider"
	"github.com/devsy-org/devsy/pkg/workspace"
	oldlog "github.com/devsy-org/log"
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
func NewImportCmd(flags *flags.GlobalFlags) *cobra.Command {
	cmd := &ImportCmd{
		GlobalFlags: flags,
	}
	importCmd := &cobra.Command{
		Use:    "import",
		Short:  "Imports a workspace configuration",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cobraCmd *cobra.Command, args []string) error {
			devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}

			return cmd.Run(cobraCmd.Context(), devsyConfig, oldlog.Default)
		},
	}

	importCmd.Flags().StringVar(&cmd.WorkspaceID, "workspace-id", "", "To workspace id to use")
	importCmd.Flags().StringVar(&cmd.MachineID, "machine-id", "", "The machine id to use")
	importCmd.Flags().
		BoolVar(&cmd.MachineReuse, "machine-reuse", false, "If machine already exists, reuse existing machine")
	importCmd.Flags().StringVar(&cmd.ProviderID, "provider-id", "", "The provider id to use")
	importCmd.Flags().
		BoolVar(&cmd.ProviderReuse, "provider-reuse", false, "If provider already exists, reuse existing provider")
	importCmd.Flags().StringVar(&cmd.Data, "data", "", "The data to import as raw json")
	_ = importCmd.MarkFlagRequired("data")
	return importCmd
}

//nolint:cyclop // pre-existing complexity
func (cmd *ImportCmd) Run(
	ctx context.Context,
	devsyConfig *config.Config,
	log oldlog.Logger,
) error {
	exportConfig := &provider.ExportConfig{}
	err := json.Unmarshal([]byte(cmd.Data), exportConfig)
	if err != nil {
		return fmt.Errorf("decode workspace data: %w", err)
	} else if exportConfig.Workspace == nil {
		return fmt.Errorf("workspace is missing in imported data")
	} else if exportConfig.Provider == nil {
		return fmt.Errorf("provider is missing in imported data")
	}

	// set ids correctly
	if cmd.MachineID == "" && exportConfig.Machine != nil {
		cmd.MachineID = exportConfig.Machine.ID
	}
	if cmd.WorkspaceID == "" {
		cmd.WorkspaceID = exportConfig.Workspace.ID
	}
	if cmd.ProviderID == "" {
		cmd.ProviderID = exportConfig.Provider.ID
	}

	// check if conflicting ids
	err = cmd.checkForConflictingIDs(ctx, exportConfig, devsyConfig, log)
	if err != nil {
		return err
	}

	// import provider
	err = cmd.importProvider(devsyConfig, exportConfig, log)
	if err != nil {
		return err
	}

	// import machine
	err = cmd.importMachine(devsyConfig, exportConfig, log)
	if err != nil {
		return err
	}

	// import workspace
	err = cmd.importWorkspace(devsyConfig, exportConfig, log)
	if err != nil {
		return err
	}

	return nil
}

func (cmd *ImportCmd) importWorkspace(
	devsyConfig *config.Config,
	exportConfig *provider.ExportConfig,
	log oldlog.Logger,
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

	// save machine config
	err = provider.SaveWorkspaceConfig(workspaceConfig)
	if err != nil {
		return fmt.Errorf("save workspace config: %w", err)
	}

	log.Donef("imported workspace: workspaceId=%s", cmd.WorkspaceID)
	return nil
}

func (cmd *ImportCmd) importMachine(
	devsyConfig *config.Config,
	exportConfig *provider.ExportConfig,
	log oldlog.Logger,
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

	// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
	err = os.MkdirAll(machineDir, 0o755)
	if err != nil {
		return fmt.Errorf("create machine dir: %w", err)
	}

	decoded, err := base64.RawStdEncoding.DecodeString(exportConfig.Machine.Data)
	if err != nil {
		return fmt.Errorf("decode machine data: %w", err)
	}

	err = extract.Extract(bytes.NewReader(decoded), machineDir)
	if err != nil {
		return fmt.Errorf("extract machine data: %w", err)
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

	log.Donef("imported machine: machineId=%s", cmd.MachineID)
	return nil
}

func (cmd *ImportCmd) importProvider(
	devsyConfig *config.Config,
	exportConfig *provider.ExportConfig,
	log oldlog.Logger,
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

	// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
	err = os.MkdirAll(providerDir, 0o755)
	if err != nil {
		return fmt.Errorf("create provider dir: %w", err)
	}

	decoded, err := base64.RawStdEncoding.DecodeString(exportConfig.Provider.Data)
	if err != nil {
		return fmt.Errorf("decode provider data: %w", err)
	}

	err = extract.Extract(bytes.NewReader(decoded), providerDir)
	if err != nil {
		return fmt.Errorf("extract provider data: %w", err)
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

	// add provider options
	if exportConfig.Provider.Config != nil {
		if devsyConfig.Current().Providers == nil {
			devsyConfig.Current().Providers = map[string]*config.ProviderConfig{}
		}

		devsyConfig.Current().Providers[cmd.ProviderID] = exportConfig.Provider.Config
		err = config.SaveConfig(devsyConfig)
		if err != nil {
			return fmt.Errorf("save devsy config: %w", err)
		}
	}

	log.Donef("imported provider: providerId=%s", cmd.ProviderID)
	return nil
}

func (cmd *ImportCmd) checkForConflictingIDs(
	ctx context.Context,
	exportConfig *provider.ExportConfig,
	devsyConfig *config.Config,
	log oldlog.Logger,
) error {
	workspaces, err := workspace.List(ctx, devsyConfig, false, cmd.Owner)
	if err != nil {
		return fmt.Errorf("error listing workspaces: %w", err)
	}

	// check for workspace duplicate
	if exportConfig.Workspace != nil {
		for _, workspace := range workspaces {
			if workspace.ID == cmd.WorkspaceID {
				return fmt.Errorf(
					"existing workspace with id %s found, please use --workspace-id to override the workspace id",
					cmd.WorkspaceID,
				)
			} else if workspace.UID == exportConfig.Workspace.UID {
				return fmt.Errorf(
					"existing workspace %s with uid %s found, please use --workspace-id to override the workspace id",
					workspace.ID,
					workspace.UID,
				)
			}
		}
	}

	// check if machine already exists
	if !cmd.MachineReuse && exportConfig.Machine != nil {
		if provider.MachineExists(devsyConfig.DefaultContext, cmd.MachineID) {
			return fmt.Errorf(
				"existing machine with id %s found, please use --machine-reuse to skip importing "+
					"the machine or --machine-id to override the machine id",
				cmd.MachineID,
			)
		}
	}

	// check if provider already exists
	if !cmd.ProviderReuse && exportConfig.Provider != nil {
		if provider.ProviderExists(devsyConfig.DefaultContext, cmd.ProviderID) {
			return fmt.Errorf(
				"existing provider with id %s found, please use --provider-reuse to skip importing "+
					"the provider or --provider-id to override the provider id",
				cmd.ProviderID,
			)
		}
	}

	return nil
}
