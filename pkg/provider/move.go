package provider

import (
	"fmt"
	"os"
	"strings"

	"github.com/devsy-org/devsy/pkg/config"
)

// MoveProvider renames a provider's directory on disk and migrates its state
// in config.json. This preserves all options, initialized flag, and other state
// that CloneProvider would lose.
func MoveProvider(devsyConfig *config.Config, oldName, newName string) error {
	oldDir, err := GetProviderDir(devsyConfig.DefaultContext, oldName)
	if err != nil {
		return fmt.Errorf("get old provider dir: %w", err)
	}

	newDir, err := GetProviderDir(devsyConfig.DefaultContext, newName)
	if err != nil {
		return fmt.Errorf("get new provider dir: %w", err)
	}

	if err := os.Rename(oldDir, newDir); err != nil {
		return fmt.Errorf("rename provider dir: %w", err)
	}

	if err := updateProviderConfigName(devsyConfig, newName); err != nil {
		_ = os.Rename(newDir, oldDir)
		return err
	}

	if err := migrateProviderState(devsyConfig, oldName, newName); err != nil {
		// Revert the config name before moving the directory back to avoid
		// a corrupted provider.json (dir at old path but name field says new name).
		_ = revertProviderConfigName(devsyConfig, oldName, newName)
		_ = os.Rename(newDir, oldDir)
		return err
	}

	return nil
}

// updateProviderConfigName loads the provider config from the new directory and
// updates its Name field to match the new directory name.
func updateProviderConfigName(devsyConfig *config.Config, newName string) error {
	providerConfig, err := LoadProviderConfig(devsyConfig.DefaultContext, newName)
	if err != nil {
		return fmt.Errorf("load provider config after move: %w", err)
	}
	providerConfig.Name = newName
	if err := SaveProviderConfig(devsyConfig.DefaultContext, providerConfig); err != nil {
		return fmt.Errorf("save provider config: %w", err)
	}
	return nil
}

// revertProviderConfigName restores the provider config Name field back to
// oldName. This is used during rollback when migrateProviderState fails after
// updateProviderConfigName has already written the new name.
func revertProviderConfigName(devsyConfig *config.Config, oldName, newName string) error {
	providerConfig, err := LoadProviderConfig(devsyConfig.DefaultContext, newName)
	if err != nil {
		return fmt.Errorf("load provider config for revert: %w", err)
	}
	providerConfig.Name = oldName
	return SaveProviderConfig(devsyConfig.DefaultContext, providerConfig)
}

// migrateProviderState moves the provider's entry in the config Providers map
// from oldName to newName, rewrites any option values that embed the old
// provider directory path, and persists the change.
func migrateProviderState(devsyConfig *config.Config, oldName, newName string) error {
	ctx := devsyConfig.Current()
	if ctx.Providers[oldName] == nil {
		return nil
	}

	ctx.Providers[newName] = ctx.Providers[oldName]
	delete(ctx.Providers, oldName)

	// Rewrite option values that reference the old provider directory.
	oldDir, _ := GetProviderDir(devsyConfig.DefaultContext, oldName)
	newDir, _ := GetProviderDir(devsyConfig.DefaultContext, newName)
	if oldDir != "" && newDir != "" {
		rewriteOptionPaths(ctx.Providers[newName].Options, oldDir, newDir)
	}

	if err := config.SaveConfig(devsyConfig); err != nil {
		// Undo the map move and path rewrite on failure.
		if oldDir != "" && newDir != "" {
			rewriteOptionPaths(ctx.Providers[newName].Options, newDir, oldDir)
		}
		ctx.Providers[oldName] = ctx.Providers[newName]
		delete(ctx.Providers, newName)
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// rewriteOptionPaths replaces occurrences of oldDir with newDir in every
// option value. This keeps absolute paths (e.g. SSH key paths) valid after
// a provider directory rename.
func rewriteOptionPaths(opts map[string]config.OptionValue, oldDir, newDir string) {
	for k, v := range opts {
		if strings.Contains(v.Value, oldDir) {
			v.Value = strings.ReplaceAll(v.Value, oldDir, newDir)
			opts[k] = v
		}
	}
}
