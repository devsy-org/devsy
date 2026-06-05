package devsyconfig

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/platform"
	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/provider"
)

type DevsyConfigResponse struct {
	DevsyConfig *client.Config
}

func Read(workspace *provider.Workspace) (*DevsyConfigResponse, error) {
	cfg, err := readConfig(workspace.Context, workspace.Provider.Name)
	if err != nil {
		return nil, err
	}

	return &DevsyConfigResponse{DevsyConfig: cfg}, nil
}

func readConfig(contextName string, providerName string) (*client.Config, error) {
	providerDir, err := provider.GetProviderDir(contextName, providerName)
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(providerDir, platform.DevsyPlatformConfigFileName)

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return &client.Config{}, nil
	}

	content, err := os.ReadFile(
		configPath,
	) // #nosec G304 -- path is provider dir + constant filename
	if err != nil {
		return nil, err
	}

	cfg := &client.Config{}
	err = json.Unmarshal(content, cfg)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}
