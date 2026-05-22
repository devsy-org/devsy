//go:build linux

package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type linuxPathManager struct {
	basePathManager
}

func newPlatformPathManager() PathManager {
	pm := &linuxPathManager{}
	pm.pm = pm

	return pm
}

func (l *linuxPathManager) ConfigDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config dir: %w", err)
	}

	return ensureDir(filepath.Join(home, "."+RepoName))
}

func (l *linuxPathManager) DataDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("data dir: %w", err)
	}

	return ensureDir(filepath.Join(home, "."+RepoName))
}

func (l *linuxPathManager) CacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cache dir: %w", err)
	}

	return ensureDir(filepath.Join(home, ".cache", RepoName))
}

func (l *linuxPathManager) StateDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("state dir: %w", err)
	}

	return ensureDir(filepath.Join(home, "."+RepoName, "state"))
}

func (l *linuxPathManager) RuntimeDir() (string, error) {
	return ensureDir(filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", RepoName, os.Getuid())))
}
