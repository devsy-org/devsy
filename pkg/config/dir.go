package config

import (
	"os"
	"path/filepath"

	"github.com/devsy-org/devsy/pkg/util"
)

// ConfigDirName is the hidden directory name used for Devsy configuration.
const ConfigDirName = "." + RepoName

func GetConfigDir() (string, error) {
	homeDir := os.Getenv(EnvHome)
	if homeDir != "" {
		return homeDir, nil
	}

	homeDir, err := util.UserHomeDir()
	if err != nil {
		return "", err
	}

	configDir := filepath.Join(homeDir, ConfigDirName)
	return configDir, nil
}

func GetConfigPath() (string, error) {
	configOrigin := os.Getenv(EnvConfig)
	if configOrigin == "" {
		configDir, err := GetConfigDir()
		if err != nil {
			return "", err
		}

		return filepath.Join(configDir, ConfigFile), nil
	}

	return configOrigin, nil
}
