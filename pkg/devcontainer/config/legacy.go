package config

// replaceLegacy migrates deprecated top-level fields (Extensions, Settings,
// DevPort) into customizations.vscode, where current devcontainer configs
// expect them. It is a no-op when none of the legacy fields are set.
func replaceLegacy(config *DevContainerConfig) (*DevContainerConfig, error) {
	if len(config.Extensions) == 0 && len(config.Settings) == 0 && config.DevPort == 0 {
		return config, nil
	}

	if config.Customizations == nil {
		config.Customizations = map[string]any{}
	}

	vsCodeConfig := &VSCodeCustomizations{}
	if vscode, ok := config.Customizations["vscode"]; ok {
		if err := convert(vscode, &vsCodeConfig); err != nil {
			return nil, err
		}
	}

	migrateLegacyExtensions(config, vsCodeConfig)
	migrateLegacySettings(config, vsCodeConfig)
	migrateLegacyDevPort(config, vsCodeConfig)

	config.Customizations["vscode"] = vsCodeConfig
	return config, nil
}

func migrateLegacyExtensions(config *DevContainerConfig, vsCode *VSCodeCustomizations) {
	if len(config.Extensions) == 0 {
		return
	}
	// Append legacy extensions to any new-style ones rather than replacing, and
	// de-duplicate so a config that sets both does not lose entries.
	seen := make(map[string]bool, len(vsCode.Extensions))
	for _, ext := range vsCode.Extensions {
		seen[ext] = true
	}
	for _, ext := range config.Extensions {
		if !seen[ext] {
			vsCode.Extensions = append(vsCode.Extensions, ext)
			seen[ext] = true
		}
	}
	config.Extensions = nil
}

func migrateLegacySettings(config *DevContainerConfig, vsCode *VSCodeCustomizations) {
	if len(config.Settings) == 0 {
		return
	}
	if vsCode.Settings == nil {
		vsCode.Settings = map[string]any{}
	}
	for k, v := range config.Settings {
		if _, exists := vsCode.Settings[k]; !exists {
			vsCode.Settings[k] = v
		}
	}
	config.Settings = nil
}

func migrateLegacyDevPort(config *DevContainerConfig, vsCode *VSCodeCustomizations) {
	if config.DevPort == 0 {
		return
	}
	// Only backfill when the new-style value is unset, but always clear the
	// deprecated field so it is not re-emitted on save.
	if vsCode.DevPort == 0 {
		vsCode.DevPort = config.DevPort
	}
	config.DevPort = 0
}
