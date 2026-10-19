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

func newPlatformPathManager(readOnly bool) PathManager {
	pm := &unixPathManager{basePathManager: basePathManager{readOnly: readOnly}}
	pm.pm = pm
	return pm
}

func (u *unixPathManager) ConfigDir() (string, error) {
	if dir, ok, err := u.homeOverrideDir(); ok {
		return dir, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config dir: %w", err)
	}
	return u.ensureDir(filepath.Join(home, "."+RepoName))
}

func (u *unixPathManager) DataDir() (string, error) {
	if dir, ok, err := u.homeOverrideDir(); ok {
		return dir, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("data dir: %w", err)
	}
	return u.ensureDir(filepath.Join(home, "."+RepoName))
}

func (u *unixPathManager) CacheDir() (string, error) {
	if dir, ok, err := u.homeOverrideDir("cache"); ok {
		return dir, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cache dir: %w", err)
	}
	return u.ensureDir(filepath.Join(home, ".cache", RepoName))
}

func (u *unixPathManager) StateDir() (string, error) {
	if dir, ok, err := u.homeOverrideDir("state"); ok {
		return dir, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("state dir: %w", err)
	}
	return u.ensureDir(filepath.Join(home, "."+RepoName, "state"))
}

func (u *unixPathManager) RuntimeDir() (string, error) {
	if dir, ok, err := u.homeOverrideDir("run"); ok {
		return dir, err
	}
	return u.ensureDir(filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", RepoName, os.Getuid())))
}

func (u *unixPathManager) SystemBinDir() (string, error) {
	return "/usr/local/bin", nil
}
