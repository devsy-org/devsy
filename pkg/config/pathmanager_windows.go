//go:build windows

package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type windowsPathManager struct {
	basePathManager
}

func newPlatformPathManager() PathManager {
	pm := &windowsPathManager{}
	pm.basePathManager.pm = pm

	return pm
}

func (w *windowsPathManager) ConfigDir() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", fmt.Errorf("config dir: APPDATA environment variable is not set")
	}

	return filepath.Join(appData, RepoName), nil
}

func (w *windowsPathManager) DataDir() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return "", fmt.Errorf("data dir: LOCALAPPDATA environment variable is not set")
	}

	return filepath.Join(localAppData, RepoName), nil
}

func (w *windowsPathManager) CacheDir() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return "", fmt.Errorf("cache dir: LOCALAPPDATA environment variable is not set")
	}

	return filepath.Join(localAppData, RepoName, "cache"), nil
}

func (w *windowsPathManager) StateDir() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return "", fmt.Errorf("state dir: LOCALAPPDATA environment variable is not set")
	}

	return filepath.Join(localAppData, RepoName, "state"), nil
}

func (w *windowsPathManager) RuntimeDir() (string, error) {
	return filepath.Join(os.TempDir(), RepoName), nil
}
