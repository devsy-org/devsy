package vscode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/util"
	"github.com/tailscale/hujson"
)

const (
	settingUseExecServer  = "remote.SSH.useExecServer"
	settingConnectTimeout = "remote.SSH.connectTimeout"
)

var userSettingsDirNames = map[Flavor]string{
	FlavorStable:      "Code",
	FlavorInsiders:    "Code - Insiders",
	FlavorCursor:      "Cursor",
	FlavorPositron:    "Positron",
	FlavorCodium:      "VSCodium",
	FlavorWindsurf:    "Windsurf",
	FlavorAntigravity: "Antigravity",
	FlavorBob:         "Bob",
}

// EnsureHostSettings ensures required VS Code user settings are configured
// on the host machine. It disables exec-server mode (useExecServer=false)
// which is required for both ProxyCommand and tunnel-based SSH connections
// because VS Code needs a remote TCP port to connect to.
func EnsureHostSettings(flavor Flavor) {
	settingsPath, err := userSettingsPath(flavor)
	if err != nil {
		log.Debugf("cannot determine VS Code settings path: %v", err)
		return
	}

	required := map[string]any{
		settingUseExecServer: false,
	}

	if err := mergeUserSettings(settingsPath, required); err != nil {
		log.Debugf("failed to update VS Code host settings: %v", err)
	}
}

func ensureHostSettingsWithTimeout(flavor Flavor, value string) error {
	if !regexp.MustCompile(Options[SSHConnectTimeout].ValidationPattern).MatchString(value) {
		return fmt.Errorf("%s", Options[SSHConnectTimeout].ValidationMessage)
	}
	timeout, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("invalid SSH_CONNECT_TIMEOUT: %w", err)
	}
	path, err := userSettingsPath(flavor)
	if err != nil {
		return err
	}
	return mergeUserSettings(path, map[string]any{
		settingUseExecServer:  false,
		settingConnectTimeout: float64(timeout),
	})
}

// removeUserSetting removes a single key from the VS Code user settings file.
// It is a no-op if the file does not exist or the key is not present.
func removeUserSetting(path string, key string) {
	existing, err := readSettingsFile(path)
	if err != nil {
		log.Debugf("cannot read settings file for removal: %v", err)
		return
	}

	if _, ok := existing[key]; !ok {
		return
	}

	delete(existing, key)

	out, err := json.MarshalIndent(existing, "", "    ")
	if err != nil {
		log.Debugf("cannot marshal settings after removal: %v", err)
		return
	}

	if err := os.WriteFile(path, out, 0o600); err != nil {
		log.Debugf("cannot write settings after removal: %v", err)
	}
}

func userSettingsPath(flavor Flavor) (string, error) {
	dirName, ok := userSettingsDirNames[flavor]
	if !ok {
		dirName = userSettingsDirNames[FlavorStable]
	}

	homeDir, err := util.UserHomeDir()
	if err != nil {
		return "", err
	}

	base := userSettingsBaseDir(homeDir, runtime.GOOS)
	return filepath.Join(base, dirName, "User", "settings.json"), nil
}

func userSettingsBaseDir(homeDir, goos string) string {
	switch goos {
	case "darwin":
		return filepath.Join(homeDir, "Library", "Application Support")
	case "windows":
		if base := os.Getenv("APPDATA"); base != "" {
			return base
		}
		return filepath.Join(homeDir, "AppData", "Roaming")
	case "linux":
		if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
			return base
		}
	}
	return filepath.Join(homeDir, ".config")
}

func readSettingsFile(path string) (map[string]any, error) {
	existing := make(map[string]any)
	data, err := os.ReadFile(path) // #nosec G304 -- path is constructed internally
	if err == nil {
		normalized, err := hujson.Standardize(data)
		if err != nil {
			return nil, fmt.Errorf("normalize settings: %w", err)
		}
		if err := json.Unmarshal(normalized, &existing); err != nil {
			return nil, fmt.Errorf("parse settings: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return existing, nil
}

func mergeUserSettings(path string, required map[string]any) error {
	existing, err := readSettingsFile(path)
	if err != nil {
		return err
	}

	changed := false
	for key, val := range required {
		if existing[key] != val {
			existing[key] = val
			changed = true
		}
	}

	if !changed {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}

	out, err := json.MarshalIndent(existing, "", "    ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, out, 0o600)
}
