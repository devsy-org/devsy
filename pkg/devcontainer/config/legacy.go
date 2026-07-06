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
	vsCode.Extensions = config.Extensions
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
	if vsCode.DevPort != 0 {
		return
	}
	vsCode.DevPort = config.DevPort
	config.DevPort = 0
}
