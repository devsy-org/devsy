package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/cmd/flags"
	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/extract"
	cliflags "github.com/devsy-org/devsy/pkg/flags"
	"github.com/devsy-org/devsy/pkg/flags/names"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
	snapshotpkg "github.com/devsy-org/devsy/pkg/snapshot"
	"github.com/devsy-org/devsy/pkg/status"
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
	exportConfig *provider.ExportConfig,
) error {
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
	reporter, err := newWorkspaceStatusReporter(
		cmd.ResultFormat,
		os.Stdout,
		cmd.Verbosity > 0 || cmd.Debug,
		cmd.NoStatus,
	)
	if err != nil {
		return err
	}
	exportConfig, parseErr := cmd.parseExportConfig()
	if parseErr == nil {
		cmd.setDefaultIDs(exportConfig)
		reporter = withWorkspaceJournal(reporter, cmd.WorkspaceID)
	}
	return status.Run(
		ctx,
		reporter,
		status.Operation{Phase: status.PhaseImportingWorkspace},
		func(ctx context.Context) error {
			if parseErr != nil {
				return parseErr
			}
			unlock, err := config.LockConfig()
			if err != nil {
				return err
			}
			defer unlock()

			devsyConfig, err := config.LoadConfig(cmd.Context, cmd.Provider)
			if err != nil {
				return err
			}
			return cmd.Run(ctx, devsyConfig, exportConfig)
		},
	)
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
	local, err := loadLocalWorkspace(devsyConfig, cmd.WorkspaceID)
	if err != nil {
		return err
	}
	workspaceDir, err := provider.GetWorkspaceDir(devsyConfig.DefaultContext, cmd.WorkspaceID)
	if err != nil {
		return fmt.Errorf("get workspace dir: %w", err)
	}

	stageDir, err := stageWorkspaceData(devsyConfig, exportConfig.Workspace.Data)
	if err != nil {
		return err
	}
	defer removeStage(stageDir)

	imported, err := readStagedWorkspace(stageDir)
	if err != nil {
		return err
	}
	if err := cmd.prepareImportedWorkspace(devsyConfig, exportConfig, local, imported); err != nil {
		return err
	}
	if err := writeStagedWorkspace(stageDir, imported); err != nil {
		return err
	}
	if err := commitStagedWorkspace(stageDir, workspaceDir); err != nil {
		return err
	}

	log.Infof("imported workspace: workspaceId=%s", cmd.WorkspaceID)
	return nil
}

func (cmd *ImportCmd) prepareImportedWorkspace(
	devsyConfig *config.Config,
	exportConfig *provider.ExportConfig,
	local, imported *provider.Workspace,
) error {
	imported.ID = cmd.WorkspaceID
	imported.Context = devsyConfig.DefaultContext
	imported.Machine.ID = cmd.MachineID
	imported.Provider.Name = cmd.ProviderID
	applyImportedOwnership(devsyConfig, local, imported)

	if exportConfig.SnapshotRef != "" {
		return applySnapshotSource(imported, exportConfig.SnapshotRef)
	}
	return nil
}

// applyImportedOwnership makes the destination the owner of the record. A
// local record with the same UID keeps its own presentation and SSH config
// locations. Otherwise the imported label and alias are validated against the
// destination and the exporter's SSH config paths are discarded.
func applyImportedOwnership(devsyConfig *config.Config, local, imported *provider.Workspace) {
	if local != nil && local.UID == imported.UID {
		imported.DisplayName = local.DisplayName
		imported.SSHHostAlias = local.SSHHostAlias
		imported.SSHConfigPath = local.SSHConfigPath
		imported.SSHConfigIncludePath = local.SSHConfigIncludePath
		return
	}

	imported.DisplayName, imported.SSHHostAlias = importedPresentation(devsyConfig, imported)
	imported.SSHConfigPath = ""
	imported.SSHConfigIncludePath = ""
}

func importedPresentation(
	devsyConfig *config.Config,
	imported *provider.Workspace,
) (string, string) {
	displayName := imported.DisplayName
	if normalized, err := workspace.NormalizeDisplayName(displayName); err != nil ||
		workspace.ValidatePresentation(
			devsyConfig,
			workspace.PresentationCandidate{ID: imported.ID, DisplayName: normalized},
			workspace.SSHConfigLocation{},
		) != nil {
		log.Warnf("dropping imported display name %q", displayName)
		displayName = ""
	} else {
		displayName = normalized
	}

	sshHostAlias := imported.SSHHostAlias
	if normalized, err := workspace.NormalizeSSHHostAlias(sshHostAlias); err != nil ||
		workspace.ValidatePresentation(
			devsyConfig,
			workspace.PresentationCandidate{ID: imported.ID, SSHHostAlias: normalized},
			workspace.SSHConfigLocation{},
		) != nil {
		log.Warnf("dropping imported ssh host alias %q", sshHostAlias)
		sshHostAlias = ""
	} else {
		sshHostAlias = normalized
	}

	return displayName, sshHostAlias
}

func loadLocalWorkspace(
	devsyConfig *config.Config,
	workspaceID string,
) (*provider.Workspace, error) {
	local, err := provider.LoadWorkspaceConfig(devsyConfig.DefaultContext, workspaceID)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load existing workspace config: %w", err)
	}
	return local, nil
}

// stageWorkspaceData extracts the base64-encoded, archived workspace data into
// a hidden staging dir next to the workspace dirs, so nothing is committed to
// a workspace path until the imported record has been validated.
func stageWorkspaceData(devsyConfig *config.Config, data string) (string, error) {
	workspacesDir, err := provider.GetWorkspacesDir(devsyConfig.DefaultContext)
	if err != nil {
		return "", fmt.Errorf("get workspaces dir: %w", err)
	}

	decoded, err := base64.RawStdEncoding.DecodeString(data)
	if err != nil {
		return "", fmt.Errorf("decode workspace data: %w", err)
	}

	// #nosec G301
	if err := os.MkdirAll(workspacesDir, 0o755); err != nil {
		return "", fmt.Errorf("create workspaces dir: %w", err)
	}
	stageDir, err := os.MkdirTemp(workspacesDir, ".import-")
	if err != nil {
		return "", fmt.Errorf("create import staging dir: %w", err)
	}

	if err := extract.Extract(bytes.NewReader(decoded), stageDir); err != nil {
		removeStage(stageDir)
		return "", fmt.Errorf("extract workspace data: %w", err)
	}
	return stageDir, nil
}

func removeStage(stageDir string) {
	if err := os.RemoveAll(stageDir); err != nil {
		log.Warnf("remove import staging dir %s: %v", stageDir, err)
	}
}

func readStagedWorkspace(stageDir string) (*provider.Workspace, error) {
	data, err := os.ReadFile(filepath.Clean(filepath.Join(stageDir, provider.WorkspaceConfigFile)))
	if err != nil {
		return nil, fmt.Errorf("read imported workspace config: %w", err)
	}

	imported := &provider.Workspace{}
	if err := json.Unmarshal(data, imported); err != nil {
		return nil, fmt.Errorf("decode imported workspace config: %w", err)
	}
	return imported, nil
}

func writeStagedWorkspace(stageDir string, imported *provider.Workspace) error {
	data, err := json.Marshal(imported)
	if err != nil {
		return fmt.Errorf("encode workspace config: %w", err)
	}

	configPath := filepath.Join(stageDir, provider.WorkspaceConfigFile)
	if err := provider.WriteFileAtomic(configPath, data, 0o644); err != nil {
		return fmt.Errorf("save workspace config: %w", err)
	}
	return nil
}

// commitStagedWorkspace moves the staging dir into workspaceDir. A new
// workspace is renamed into place in one step. An existing workspace dir keeps
// its local-only files: staged files are moved over it and the validated
// workspace.json is moved last.
func commitStagedWorkspace(stageDir, workspaceDir string) error {
	// #nosec G302
	if err := os.Chmod(stageDir, 0o755); err != nil {
		return fmt.Errorf("set workspace dir permissions: %w", err)
	}

	renameErr := os.Rename(stageDir, workspaceDir)
	if renameErr == nil {
		return nil
	}
	if _, err := os.Stat(workspaceDir); err != nil {
		return fmt.Errorf("move imported workspace into place: %w", renameErr)
	}

	return mergeStagedWorkspace(stageDir, workspaceDir)
}

type stagedEntry struct {
	rel   string
	isDir bool
}

func listStagedEntries(stageDir string) ([]stagedEntry, error) {
	var entries []stagedEntry
	err := filepath.WalkDir(stageDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(stageDir, path)
		if err != nil {
			return err
		}
		if rel != "." && rel != provider.WorkspaceConfigFile {
			entries = append(entries, stagedEntry{rel: rel, isDir: entry.IsDir()})
		}
		return nil
	})
	return entries, err
}

func mergeStagedWorkspace(stageDir, workspaceDir string) error {
	entries, err := listStagedEntries(stageDir)
	if err != nil {
		return fmt.Errorf("list imported workspace files: %w", err)
	}

	for _, entry := range entries {
		target := filepath.Join(workspaceDir, entry.rel)
		if entry.isDir {
			// #nosec G301
			err = os.MkdirAll(target, 0o755)
		} else {
			err = os.Rename(filepath.Join(stageDir, entry.rel), target)
		}
		if err != nil {
			return fmt.Errorf("merge imported workspace: %w", err)
		}
	}

	err = os.Rename(
		filepath.Join(stageDir, provider.WorkspaceConfigFile),
		filepath.Join(workspaceDir, provider.WorkspaceConfigFile),
	)
	if err != nil {
		return fmt.Errorf("commit imported workspace config: %w", err)
	}
	return nil
}

// applySnapshotSource resolves snapshotRef into a workspace source and dev
// container source, applying both to workspaceConfig.
func applySnapshotSource(workspaceConfig *provider.Workspace, snapshotRef string) error {
	sourceStr, devContainerSource, err := snapshotpkg.RestoreComposition(snapshotRef)
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
		log.Infof("reusing existing machine %s", cmd.MachineID)
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
		log.Infof("reusing existing provider %s", cmd.ProviderID)
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
	// The icon cache is local to the exporting machine's context; the imported
	// provider falls back to bundled artwork until it is next updated.
	providerConfig.IconRevision = ""

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
