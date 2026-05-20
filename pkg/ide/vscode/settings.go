package vscode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/util"
	"github.com/tailscale/hujson"
)

const settingUseExecServer = "remote.SSH.useExecServer"

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
// on the host machine. This is needed to disable exec-server mode which is
// incompatible with ProxyCommand-based SSH connections.
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

func userSettingsPath(flavor Flavor) (string, error) {
	dirName, ok := userSettingsDirNames[flavor]
	if !ok {
		dirName = userSettingsDirNames[FlavorStable]
	}

	homeDir, err := util.UserHomeDir()
	if err != nil {
		return "", err
	}

	var base string
	switch runtime.GOOS {
	case "darwin":
		base = filepath.Join(homeDir, "Library", "Application Support")
	case "windows":
		base = os.Getenv("APPDATA")
		if base == "" {
			base = filepath.Join(homeDir, "AppData", "Roaming")
		}
	default:
		base = filepath.Join(homeDir, ".config")
	}

	return filepath.Join(base, dirName, "User", "settings.json"), nil
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
