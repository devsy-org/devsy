package devsyconfig

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/provider"
)

const (
	LoftPlatformConfigFileName = "devsy-config.json" // TODO: move somewhere else, replace hardoced strings with usage of this const
)

type DevsyConfigRequest struct {
	// Deprecated. Do not use anymore
	Context string
	// Deprecated. Do not use anymore
	Provider string
}

type DevsyConfigResponse struct {
	DevsyConfig *client.Config
}

func Read(request *DevsyConfigRequest) (*DevsyConfigResponse, error) {
	loftConfig, err := readConfig(request.Context, request.Provider)
	if err != nil {
		return nil, err
	}

	return &DevsyConfigResponse{DevsyConfig: loftConfig}, nil
}

func ReadFromWorkspace(workspace *provider.Workspace) (*DevsyConfigResponse, error) {
	loftConfig, err := readConfig(workspace.Context, workspace.Provider.Name)
	if err != nil {
		return nil, err
	}

	return &DevsyConfigResponse{DevsyConfig: loftConfig}, nil
}

func readConfig(contextName string, providerName string) (*client.Config, error) {
	providerDir, err := provider.GetProviderDir(contextName, providerName)
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(providerDir, LoftPlatformConfigFileName)

	// Check if given context and provider have Loft Platform configuration
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// If not just return empty response
		return &client.Config{}, nil
	}

	content, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	loftConfig := &client.Config{}
	err = json.Unmarshal(content, loftConfig)
	if err != nil {
		return nil, err
	}

	return loftConfig, nil
}
