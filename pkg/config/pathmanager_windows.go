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
	pm.pm = pm

	return pm
}

func (w *windowsPathManager) ConfigDir() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", fmt.Errorf("config dir: APPDATA environment variable is not set")
	}

	return ensureDir(filepath.Join(appData, RepoName))
}

func (w *windowsPathManager) DataDir() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return "", fmt.Errorf("data dir: LOCALAPPDATA environment variable is not set")
	}

	return ensureDir(filepath.Join(localAppData, RepoName))
}

func (w *windowsPathManager) CacheDir() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return "", fmt.Errorf("cache dir: LOCALAPPDATA environment variable is not set")
	}

	return ensureDir(filepath.Join(localAppData, RepoName, "cache"))
}

func (w *windowsPathManager) StateDir() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return "", fmt.Errorf("state dir: LOCALAPPDATA environment variable is not set")
	}

	return ensureDir(filepath.Join(localAppData, RepoName, "state"))
}

func (w *windowsPathManager) RuntimeDir() (string, error) {
	return ensureDir(filepath.Join(os.TempDir(), RepoName))
}

// SystemBinDir returns a PATH directory for installing standalone tool binaries.
// %LOCALAPPDATA%\Microsoft\WindowsApps is on PATH by default.
func (w *windowsPathManager) SystemBinDir() (string, error) {
	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		return "", fmt.Errorf("system bin dir: LOCALAPPDATA environment variable is not set")
	}

	return filepath.Join(localAppData, "Microsoft", "WindowsApps"), nil
}
