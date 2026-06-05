package platform

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/devsy-org/devsy/pkg/platform/client"
	"github.com/devsy-org/devsy/pkg/provider"
)

const (
	defaultTimeout                     = 10 * time.Minute
	DevsyPlatformConfigFileName string = "devsy-config.json"
)

func Timeout() time.Duration {
	if timeout := os.Getenv(TimeoutEnv); timeout != "" {
		if parsedTimeout, err := time.ParseDuration(timeout); err == nil {
			return parsedTimeout
		}
	}

	return defaultTimeout
}

// ReadConfig reads client.Config for given context and provider.
func ReadConfig(contextName string, providerName string) (*client.Config, error) {
	// contextName is allowed to be empty
	providerDir, err := provider.GetProviderDir(contextName, providerName)
	if err != nil {
		return nil, err
	}

	configPath := filepath.Join(providerDir, DevsyPlatformConfigFileName)

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, err
	}

	content, err := os.ReadFile(configPath)
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
