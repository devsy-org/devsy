package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Mode string

const (
	goosLinux              = "linux"
	goosWindows            = "windows"
	githubActionsTrue      = "true"
	ModeRootless      Mode = "rootless"
	ModeRootful       Mode = "rootful"
	busyboxImage           = "busybox@sha256:fd8d9aa63ba2f0982b5304e1ee8d3b90a210bc1ffb5314d980eb6962f1a9715d"
	rootfulSocket          = "unix:///run/podman/podman.sock"
)

type SetupConfig struct {
	Mode             Mode
	PodmanPath       string
	CrunPath         string
	BootstrapTimeout time.Duration
	GitHubEnvPath    string
}

func validateConfig(cfg SetupConfig, goos string) error {
	if cfg.Mode != ModeRootless && cfg.Mode != ModeRootful {
		return usageError("invalid mode %q: expected rootless or rootful", cfg.Mode)
	}
	if cfg.BootstrapTimeout <= 0 {
		return usageError("bootstrap timeout must be positive")
	}
	if err := validatePlatformConfig(cfg, goos); err != nil {
		return err
	}
	return validatePaths(cfg, goos)
}

func validatePlatformConfig(cfg SetupConfig, goos string) error {
	switch goos {
	case goosWindows:
		return validateWindowsConfig(cfg)
	case goosLinux:
		return validateLinuxConfig(cfg)
	}
	return nil
}

func validateWindowsConfig(cfg SetupConfig) error {
	if cfg.Mode != ModeRootful {
		return usageError("Windows Podman setup only supports rootful mode")
	}
	if cfg.BootstrapTimeout > 270*time.Second {
		return usageError("Windows bootstrap timeout must not exceed 270s")
	}
	return nil
}

func validateLinuxConfig(cfg SetupConfig) error {
	if cfg.CrunPath == "" {
		return usageError("--crun-path is required on Linux")
	}
	if cfg.Mode == ModeRootful && os.Getenv("GITHUB_ACTIONS") == githubActionsTrue &&
		cfg.GitHubEnvPath == "" {
		return usageError(
			"--github-env or GITHUB_ENV is required for rootful setup in GitHub Actions",
		)
	}
	return nil
}

func validatePaths(cfg SetupConfig, goos string) error {
	if cfg.PodmanPath == "" {
		return usageError("--podman-path is required")
	}
	if err := validateAbsolutePath("--podman-path", cfg.PodmanPath); err != nil {
		return err
	}
	if _, err := os.Stat(cfg.PodmanPath); err != nil {
		return fmt.Errorf("podman executable %q is unavailable: %w", cfg.PodmanPath, err)
	}
	if goos == goosLinux {
		return validateCrunPath(cfg.CrunPath)
	}
	return nil
}

func validateAbsolutePath(flag, path string) error {
	if strings.ContainsAny(path, "\r\n\x00") || !filepath.IsAbs(path) {
		return usageError("%s must be an absolute path without line breaks", flag)
	}
	return nil
}

func validateCrunPath(path string) error {
	if err := validateAbsolutePath("--crun-path", path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		err = fmt.Errorf("is a directory")
	}
	if err != nil {
		return fmt.Errorf("crun executable %q is unavailable: %w", path, err)
	}
	return nil
}
