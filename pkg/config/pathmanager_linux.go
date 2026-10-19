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

func newPlatformPathManager(readOnly bool) PathManager {
	pm := &linuxPathManager{basePathManager: basePathManager{readOnly: readOnly}}
	pm.pm = pm

	return pm
}

func (l *linuxPathManager) ConfigDir() (string, error) {
	if dir, ok, err := l.homeOverrideDir(); ok {
		return dir, err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config dir: %w", err)
	}

	return l.ensureDir(filepath.Join(home, "."+RepoName))
}

func (l *linuxPathManager) DataDir() (string, error) {
	if dir, ok, err := l.homeOverrideDir(); ok {
		return dir, err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("data dir: %w", err)
	}

	return l.ensureDir(filepath.Join(home, "."+RepoName))
}

func (l *linuxPathManager) CacheDir() (string, error) {
	if dir, ok, err := l.homeOverrideDir("cache"); ok {
		return dir, err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cache dir: %w", err)
	}

	return l.ensureDir(filepath.Join(home, ".cache", RepoName))
}

func (l *linuxPathManager) StateDir() (string, error) {
	if dir, ok, err := l.homeOverrideDir("state"); ok {
		return dir, err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("state dir: %w", err)
	}

	return l.ensureDir(filepath.Join(home, "."+RepoName, "state"))
}

func (l *linuxPathManager) RuntimeDir() (string, error) {
	if dir, ok, err := l.homeOverrideDir("run"); ok {
		return dir, err
	}

	return l.ensureDir(filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", RepoName, os.Getuid())))
}

func (l *linuxPathManager) SystemBinDir() (string, error) {
	return "/usr/local/bin", nil
}
