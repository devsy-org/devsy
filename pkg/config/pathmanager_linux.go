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
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return ensureDir(filepath.Join(dir, RepoName))
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("config dir: %w", err)
	}

	return ensureDir(filepath.Join(home, ".config", RepoName))
}

func (l *linuxPathManager) DataDir() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return ensureDir(filepath.Join(dir, RepoName))
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("data dir: %w", err)
	}

	return ensureDir(filepath.Join(home, ".local", "share", RepoName))
}

func (l *linuxPathManager) CacheDir() (string, error) {
	if dir := os.Getenv("XDG_CACHE_HOME"); dir != "" {
		return ensureDir(filepath.Join(dir, RepoName))
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cache dir: %w", err)
	}

	return ensureDir(filepath.Join(home, ".cache", RepoName))
}

func (l *linuxPathManager) StateDir() (string, error) {
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return ensureDir(filepath.Join(dir, RepoName))
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("state dir: %w", err)
	}

	return ensureDir(filepath.Join(home, ".local", "state", RepoName))
}

func (l *linuxPathManager) RuntimeDir() (string, error) {
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return ensureDir(filepath.Join(dir, RepoName))
	}

	return ensureDir(filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", RepoName, os.Getuid())))
}
