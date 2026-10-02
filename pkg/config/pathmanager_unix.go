//go:build unix && !linux && !darwin

package config

import (
	"fmt"
	"os"
	"path/filepath"
)

type unixPathManager struct {
	basePathManager
}

func newPlatformPathManager() PathManager {
	pm := &unixPathManager{}
	pm.pm = pm
	return pm
}

func (u *unixPathManager) ConfigDir() (string, error) {
	if dir, ok, err := homeOverrideDir(); ok {
		return dir, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config dir: %w", err)
	}
	return ensureDir(filepath.Join(home, "."+RepoName))
}

func (u *unixPathManager) DataDir() (string, error) {
	if dir, ok, err := homeOverrideDir(); ok {
		return dir, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("data dir: %w", err)
	}
	return ensureDir(filepath.Join(home, "."+RepoName))
}

func (u *unixPathManager) CacheDir() (string, error) {
	if dir, ok, err := homeOverrideDir("cache"); ok {
		return dir, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cache dir: %w", err)
	}
	return ensureDir(filepath.Join(home, ".cache", RepoName))
}

func (u *unixPathManager) StateDir() (string, error) {
	if dir, ok, err := homeOverrideDir("state"); ok {
		return dir, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("state dir: %w", err)
	}
	return ensureDir(filepath.Join(home, "."+RepoName, "state"))
}

func (u *unixPathManager) RuntimeDir() (string, error) {
	if dir, ok, err := homeOverrideDir("run"); ok {
		return dir, err
	}
	return ensureDir(filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", RepoName, os.Getuid())))
}

func (u *unixPathManager) SystemBinDir() (string, error) {
	return "/usr/local/bin", nil
}
