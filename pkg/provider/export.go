package provider

import (
	"bytes"
	"encoding/base64"
	"fmt"

	"github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/extract"
)

var excludedPaths = []string{
	".cache/",
	"cache/",
	"binaries/",
	"source/",
	".temp/",
	"temp/",
	".tmp/",
	"tmp/",
}

type ExportConfig struct {
	// Workspace is the workspace that was exported
	Workspace *ExportWorkspaceConfig `json:"workspace,omitempty"`

	// Machine is the machine that was exported
	Machine *ExportMachineConfig `json:"machine,omitempty"`

	// Provider is the provider that was exported
	Provider *ExportProviderConfig `json:"provider,omitempty"`

	// SnapshotRef, if set, is a snapshot the exported workspace's state should
	// be restored from on import, carrying container/volume state rather than
	// just metadata.
	SnapshotRef string `json:"snapshotRef,omitempty"`
}

type ExportWorkspaceConfig struct {
	// ID is the workspace id
	ID string `json:"id,omitempty"`

	// Context is the workspace context
	Context string `json:"context,omitempty"`

	// UID is used to identify this specific workspace
	UID string `json:"uid,omitempty"`

	// Data is the workspace folder data
	Data string `json:"data,omitempty"`
}

type ExportMachineConfig struct {
	// ID is the machine id
	ID string `json:"id,omitempty"`

	// Context is the machine context
	Context string `json:"context,omitempty"`

	// Data is the machine folder data
	Data string `json:"data,omitempty"`
}

type ExportProviderConfig struct {
	// ID is the provider id
	ID string `json:"id,omitempty"`

	// Context is the provider context
	Context string `json:"context,omitempty"`

	// Data is the provider folder data
	Data string `json:"data,omitempty"`

	// Config is the provider config within the config.yaml
	Config *config.ProviderConfig `json:"config,omitempty"`
}

func ExportWorkspace(context, workspaceID string) (*ExportWorkspaceConfig, error) {
	workspaceDir, err := GetWorkspaceDir(context, workspaceID)
	if err != nil {
		return nil, err
	}

	workspaceConfig, err := LoadWorkspaceConfig(context, workspaceID)
	if err != nil {
		return nil, err
	}

	data, err := archiveDirBase64(workspaceDir, "workspace")
	if err != nil {
		return nil, err
	}

	return &ExportWorkspaceConfig{
		ID:      workspaceID,
		UID:     workspaceConfig.UID,
		Context: context,
		Data:    data,
	}, nil
}

func ExportMachine(context, machineID string) (*ExportMachineConfig, error) {
	machineDir, err := GetMachineDir(context, machineID)
	if err != nil {
		return nil, err
	}

	_, err = LoadMachineConfig(context, machineID)
	if err != nil {
		return nil, err
	}

	data, err := archiveDirBase64(machineDir, "machine")
	if err != nil {
		return nil, err
	}

	return &ExportMachineConfig{
		ID:      machineID,
		Context: context,
		Data:    data,
	}, nil
}

func ExportProvider(
	devsyConfig *config.Config,
	context, providerID string,
) (*ExportProviderConfig, error) {
	providerDir, err := GetProviderDir(context, providerID)
	if err != nil {
		return nil, err
	}

	_, err = LoadProviderConfig(context, providerID)
	if err != nil {
		return nil, err
	}

	data, err := archiveDirBase64(providerDir, "provider")
	if err != nil {
		return nil, err
	}

	var providerConfig *config.ProviderConfig
	if devsyConfig != nil && devsyConfig.Contexts[context] != nil &&
		devsyConfig.Contexts[context].Providers != nil &&
		devsyConfig.Contexts[context].Providers[providerID] != nil {
		providerConfig = devsyConfig.Contexts[context].Providers[providerID]
	}

	return &ExportProviderConfig{
		ID:      providerID,
		Context: context,
		Data:    data,
		Config:  providerConfig,
	}, nil
}

// archiveDirBase64 tars dir (skipping excludedPaths) and returns it base64-encoded.
// kind names the dir in the error message ("compress <kind> dir").
func archiveDirBase64(dir, kind string) (string, error) {
	buf := &bytes.Buffer{}
	if err := extract.WriteTarExclude(buf, dir, true, excludedPaths); err != nil {
		return "", fmt.Errorf("compress %s dir: %w", kind, err)
	}

	return base64.RawStdEncoding.EncodeToString(buf.Bytes()), nil
}
