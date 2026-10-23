package provider

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
	config2 "github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/id"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/providers"
)

const (
	WorkspaceConfigFile        = "workspace.json"
	WorkspaceResultFile        = "workspace_result.json"
	MachineConfigFile          = "machine.json"
	PlatformInstanceConfigFile = "pro.json"
	ProviderConfigFile         = "provider.json"

	DaemonStateFile = config.BinaryName + "_ts.state"
)

func GetPlatformInstancesDir(context string) (string, error) {
	return config.DefaultPathManager().PlatformInstancesDir(context)
}

func GetMachinesDir(context string) (string, error) {
	return config.DefaultPathManager().MachinesDir(context)
}

func GetLocksDir(context string) (string, error) {
	return config.DefaultPathManager().LocksDir(context)
}

func GetWorkspacesDir(context string) (string, error) {
	return config.DefaultPathManager().WorkspacesDir(context)
}

func GetProvidersDir(context string) (string, error) {
	return config.DefaultPathManager().ProvidersDir(context)
}

func GetProviderDir(context, providerName string) (string, error) {
	return config.DefaultPathManager().ProviderDir(context, providerName)
}

func GetDaemonDir(context, providerName string) (string, error) {
	return config.DefaultPathManager().ProviderDaemonDir(context, providerName)
}

func GetProviderBinariesDir(context, providerName string) (string, error) {
	return config.DefaultPathManager().ProviderBinariesDir(context, providerName)
}

func GetMachineDir(context, machineID string) (string, error) {
	if err := requireID("machine id", machineID); err != nil {
		return "", err
	}

	return config.DefaultPathManager().MachineDir(context, machineID)
}

func GetWorkspaceDir(context, workspaceID string) (string, error) {
	if err := requireID("workspace id", workspaceID); err != nil {
		return "", err
	}

	return config.DefaultPathManager().WorkspaceDir(context, workspaceID)
}

// GetWorkspaceAgentDir returns the host-side per-workspace agent dir
// (.../workspaces/<id>/agent). Container-side agent code keeps using
// FindAgentHomeDir.
func GetWorkspaceAgentDir(context, workspaceID string) (string, error) {
	if err := requireID("workspace id", workspaceID); err != nil {
		return "", err
	}

	return config.DefaultPathManager().WorkspaceAgentDir(context, workspaceID)
}

// GetWorkspaceContentsDir returns the per-context parent of workspace content
// folders (.../contexts/<ctx>/contents).
func GetWorkspaceContentsDir(context string) (string, error) {
	return config.DefaultPathManager().WorkspaceContentsDir(context)
}

// GetWorkspaceContentDir returns the host-side bind-mount source for a
// workspace (.../contexts/<ctx>/contents/<id>).
func GetWorkspaceContentDir(context, workspaceID string) (string, error) {
	if err := requireID("workspace id", workspaceID); err != nil {
		return "", err
	}

	return config.DefaultPathManager().WorkspaceContentDir(context, workspaceID)
}

// GetWorkspaceLogDir returns the per-workspace log dir
// (.../workspaces/<id>/logs) used by the desktop's streaming-log store.
func GetWorkspaceLogDir(context, workspaceID string) (string, error) {
	if err := requireID("workspace id", workspaceID); err != nil {
		return "", err
	}

	return config.DefaultPathManager().WorkspaceLogDir(context, workspaceID)
}

func GetPlatformInstanceDir(context, platformInstanceHost string) (string, error) {
	if err := requireID("platform instance host", platformInstanceHost); err != nil {
		return "", err
	}

	return config.DefaultPathManager().
		PlatformInstanceDir(context, ToPlatformInstanceID(platformInstanceHost))
}

var (
	platformInstanceIDRegEx1 = regexp.MustCompile(`[^\w\-]`)
	platformInstanceIDRegEx2 = regexp.MustCompile(`[^0-9a-z\-]+`)
)

func ToPlatformInstanceID(url string) string {
	url = strings.TrimPrefix(url, "https://")
	url = strings.ToLower(url)
	url = platformInstanceIDRegEx2.ReplaceAllString(
		platformInstanceIDRegEx1.ReplaceAllString(url, "-"),
		"",
	)
	url = strings.Trim(url, "-")
	return id.SafeConcatNameMax([]string{url}, 32)
}

func WorkspaceExists(context, workspaceID string) bool {
	workspaceDir, err := GetWorkspaceDir(context, workspaceID)
	if err != nil {
		return false
	}

	_, err = os.Stat(workspaceDir)
	return err == nil
}

func PlatformInstanceExists(context, platformInstanceID string) bool {
	platformInstanceDir, err := GetPlatformInstanceDir(context, platformInstanceID)
	if err != nil {
		return false
	}

	_, err = os.Stat(platformInstanceDir)
	return err == nil
}

func SaveProviderConfig(context string, provider *ProviderConfig) error {
	providerDir, err := GetProviderDir(context, provider.Name)
	if err != nil {
		return err
	}

	return saveJSON(providerDir, ProviderConfigFile, provider, 0o600)
}

func SavePlatformInstanceConfig(context string, platformInstance *PlatformInstance) error {
	platformInstanceDir, err := GetPlatformInstanceDir(context, platformInstance.Host)
	if err != nil {
		return err
	}

	return saveJSON(platformInstanceDir, PlatformInstanceConfigFile, platformInstance, 0o600)
}

func SaveWorkspaceResult(workspace *Workspace, result *config2.Result) error {
	workspaceDir, err := GetWorkspaceDir(workspace.Context, workspace.ID)
	if err != nil {
		return err
	}

	// Unlike saveJSON, this never creates the workspace dir: a result is only
	// valid while the workspace config exists.
	if _, err := os.Stat(filepath.Join(workspaceDir, WorkspaceConfigFile)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("workspace %s no longer exists", workspace.ID)
		}
		return err
	}

	return writeJSONFile(filepath.Join(workspaceDir, WorkspaceResultFile), result, 0o600)
}

func SaveWorkspaceConfig(workspace *Workspace) error {
	workspaceDir, err := GetWorkspaceDir(workspace.Context, workspace.ID)
	if err != nil {
		return err
	}

	return saveJSON(workspaceDir, WorkspaceConfigFile, workspace, 0o644)
}

func SaveMachineConfig(machine *Machine) error {
	machineDir, err := GetMachineDir(machine.Context, machine.ID)
	if err != nil {
		return err
	}

	return saveJSON(machineDir, MachineConfigFile, machine, 0o600)
}

func MachineExists(context, machineID string) bool {
	machineDir, err := GetMachineDir(context, machineID)
	if err != nil {
		return false
	}

	_, err = os.Stat(machineDir)
	return err == nil
}

func ProviderExists(context, provider string) bool {
	providerDir, err := GetProviderDir(context, provider)
	if err != nil {
		return false
	}

	_, err = os.Stat(providerDir)
	return err == nil
}

func LoadProviderConfig(context, provider string) (*ProviderConfig, error) {
	return LoadProviderConfigWithPathManager(config.DefaultPathManager(), context, provider)
}

// LoadProviderConfigWithPathManager loads a provider snapshot with the built-in
// exec overlay. Path-resolution effects follow pm's policy; use
// config.NewReadOnlyPathManager to avoid creating roots during loading.
func LoadProviderConfigWithPathManager(
	pm config.PathManager,
	context, provider string,
) (*ProviderConfig, error) {
	if pm == nil {
		return nil, fmt.Errorf("load provider config: path manager is required")
	}
	providerDir, err := pm.ProviderDir(context, provider)
	if err != nil {
		return nil, err
	}

	providerFile := filepath.Join(providerDir, ProviderConfigFile)
	providerConfigBytes, err := os.ReadFile(providerFile)
	if err != nil {
		return nil, err
	}

	providerConfig, err := ParseProvider(bytes.NewReader(providerConfigBytes))
	if err != nil {
		return nil, err
	}

	// Refresh the exec commands of built-in providers from embedded YAML: a CLI
	// change (e.g. renaming the command wrapper) must not be shadowed by a stale
	// provider.json. Only the exec blocks are overlaid so user customizations
	// (custom --name, resolved options) survive.
	if providerConfig.Source.Internal {
		if embedded := embeddedProviderConfig(providerConfig.Source.Raw); embedded != nil {
			providerConfig.Exec = embedded.Exec
			providerConfig.Agent.Exec = embedded.Agent.Exec
		}
	}

	return providerConfig, nil
}

// embeddedProviderConfig parses the built-in provider definition for the given
// source key (e.g. "pro"), returning nil when it is not built-in or the YAML
// fails to parse. The built-in map is keyed by source id, which differs from
// the provider Name for some providers (pro -> name "devsy-pro").
func embeddedProviderConfig(sourceID string) *ProviderConfig {
	raw := providers.GetBuiltInProviders()[sourceID]
	if raw == "" {
		return nil
	}
	parsed, err := ParseProvider(strings.NewReader(raw))
	if err != nil {
		// Embedded YAML failing to parse is a build-time regression; log rather
		// than silently fall back to the stale stored config this refresh fixes.
		log.Errorf("built-in provider %q failed to parse: %v", sourceID, err)
		return nil
	}
	return parsed
}

func LoadMachineConfig(context, machineID string) (*Machine, error) {
	machineDir, err := GetMachineDir(context, machineID)
	if err != nil {
		return nil, err
	}

	machineConfigFile := filepath.Join(machineDir, MachineConfigFile)
	machineConfig, err := loadJSON[Machine](machineConfigFile)
	if err != nil {
		return nil, err
	}

	machineConfig.Context = context
	machineConfig.Origin = machineConfigFile
	return machineConfig, nil
}

func LoadPlatformInstanceConfig(context, platformInstanceHost string) (*PlatformInstance, error) {
	platformInstanceDir, err := GetPlatformInstanceDir(context, platformInstanceHost)
	if err != nil {
		return nil, err
	}

	return loadJSON[PlatformInstance](
		filepath.Join(platformInstanceDir, PlatformInstanceConfigFile),
	)
}

func LoadWorkspaceConfig(context, workspaceID string) (*Workspace, error) {
	workspaceDir, err := GetWorkspaceDir(context, workspaceID)
	if err != nil {
		return nil, err
	}

	workspaceConfigFile := filepath.Join(workspaceDir, WorkspaceConfigFile)
	workspaceConfig, err := loadJSON[Workspace](workspaceConfigFile)
	if err != nil {
		return nil, err
	}

	workspaceConfig.Context = context
	workspaceConfig.Origin = workspaceConfigFile
	return workspaceConfig, nil
}

func LoadWorkspaceResult(context, workspaceID string) (*config2.Result, error) {
	workspaceDir, err := GetWorkspaceDir(context, workspaceID)
	if err != nil {
		return nil, err
	}

	workspaceResult, err := loadJSON[config2.Result](
		filepath.Join(workspaceDir, WorkspaceResultFile),
	)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	return workspaceResult, nil
}
