//go:build darwin

package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type darwinPathManager struct {
	basePathManager
}

func newPlatformPathManager() PathManager {
	pm := &darwinPathManager{}
	pm.basePathManager.pm = pm

	return pm
}

func (d *darwinPathManager) ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config dir: %w", err)
	}

	return ensureDir(filepath.Join(home, "."+RepoName))
}

func (d *darwinPathManager) DataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("data dir: %w", err)
	}

	return ensureDir(filepath.Join(home, "."+RepoName))
}

func (d *darwinPathManager) CacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cache dir: %w", err)
	}

	return ensureDir(filepath.Join(home, ".cache", RepoName))
}

func (d *darwinPathManager) StateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("state dir: %w", err)
	}

	return ensureDir(filepath.Join(home, "."+RepoName, "state"))
}

func (d *darwinPathManager) RuntimeDir() (string, error) {
	return ensureDir(filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", RepoName, os.Getuid())))
}
