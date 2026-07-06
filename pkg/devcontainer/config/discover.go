package config

import (
	"fmt"
	"os"
	"path"
	"path/filepath"

	doublestar "github.com/bmatcuk/doublestar/v4"
)

// devcontainerDirName is the conventional directory that holds devcontainer
// configuration, per https://containers.dev/implementors/spec/#devcontainerjson.
const devcontainerDirName = ".devcontainer"

// ConfigSelector chooses a single devcontainer.json path from the candidates
// discovered under a workspace folder. It is only consulted when a folder
// contains more than one configuration (or when selection is forced).
type ConfigSelector func(candidates []string) (string, error)

// SelectByID returns a selector that picks the config whose parent directory
// name matches id (the devcontainer id, e.g. ".devcontainer/<id>/devcontainer.json").
func SelectByID(id string) ConfigSelector {
	return func(candidates []string) (string, error) {
		for _, candidate := range candidates {
			if filepath.Base(filepath.Dir(candidate)) == id {
				return candidate, nil
			}
		}
		return "", fmt.Errorf("devcontainer with ID %q not found", id)
	}
}

// SelectSingle returns a selector that rejects ambiguity: it errors when more
// than one config exists, listing the available ids so the caller can choose.
func SelectSingle(folder string) ConfigSelector {
	return func(candidates []string) (string, error) {
		switch {
		case len(candidates) == 0:
			return "", fmt.Errorf("no devcontainer configuration found")
		case len(candidates) > 1:
			ids, _ := ListDevContainerIDs(folder)
			return "", fmt.Errorf(
				"multiple devcontainer configurations found. Detected: %v",
				ids,
			)
		default:
			return candidates[0], nil
		}
	}
}

// resolveDevContainerPath locates the devcontainer.json to use for a folder.
//
// Resolution order:
//  1. an explicit relative path, when provided;
//  2. unless forceSelect is set, the conventional root configs
//     (.devcontainer/devcontainer.json then .devcontainer.json);
//  3. discovered nested configs, passed to the selector.
//
// forceSelect skips the root-config short-circuits so an explicitly requested
// config cannot be shadowed by a root config, and so a mismatched request
// errors even when a single config exists. It returns an empty path (nil error)
// when no configuration is found.
func resolveDevContainerPath(
	folder, relativePath string,
	selector ConfigSelector,
	forceSelect bool,
) (string, error) {
	if relativePath != "" {
		configPath := path.Join(filepath.ToSlash(folder), relativePath)
		if _, err := os.Stat(configPath); err != nil {
			return "", fmt.Errorf("devcontainer path %s does not exist: %w", configPath, err)
		}
		return configPath, nil
	}

	if !forceSelect {
		if rootPath, ok := findRootConfig(folder); ok {
			return rootPath, nil
		}
	}

	matches, err := findDevContainerConfigs(folder)
	if err != nil {
		return "", err
	}
	return selectMatch(matches, selector, forceSelect)
}

// selectMatch resolves the discovered candidates to a single path. It returns
// "" when there are no matches. A selector runs when forced, or when the choice
// is ambiguous; otherwise the sole match is returned.
func selectMatch(matches []string, selector ConfigSelector, forceSelect bool) (string, error) {
	switch {
	case len(matches) == 0:
		return "", nil
	case forceSelect && selector != nil:
		return selector(matches)
	case len(matches) == 1 || selector == nil:
		return matches[0], nil
	default:
		return selector(matches)
	}
}

// findRootConfig returns the conventional root config path for a folder, if one
// exists: .devcontainer/devcontainer.json takes precedence over .devcontainer.json.
func findRootConfig(folder string) (string, bool) {
	candidates := []string{
		filepath.Join(folder, devcontainerDirName, "devcontainer.json"),
		filepath.Join(folder, devcontainerDirName+".json"),
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, true
		}
	}
	return "", false
}

// findDevContainerConfigs discovers nested devcontainer configs one level deep
// under .devcontainer/<name>/devcontainer.json, falling back to a recursive glob
// for deeper layouts.
func findDevContainerConfigs(folder string) ([]string, error) {
	var configs []string

	devcontainerDir := filepath.Join(folder, devcontainerDirName)
	entries, err := os.ReadDir(devcontainerDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			configPath := filepath.Join(devcontainerDir, entry.Name(), "devcontainer.json")
			if _, err := os.Stat(configPath); err == nil {
				configs = append(configs, configPath)
			}
		}
	}

	if len(configs) == 0 {
		matches, err := doublestar.FilepathGlob(
			filepath.ToSlash(filepath.Clean(folder)) + "/.devcontainer/**/devcontainer.json",
		)
		if err != nil {
			return nil, err
		}
		configs = matches
	}

	return configs, nil
}

// ListDevContainerIDs returns the available devcontainer ids in a folder (the
// nested config directory names), excluding the conventional root directory.
func ListDevContainerIDs(folder string) ([]string, error) {
	configs, err := findDevContainerConfigs(folder)
	if err != nil {
		return nil, err
	}

	var ids []string
	for _, configPath := range configs {
		id := filepath.Base(filepath.Dir(configPath))
		if id != devcontainerDirName {
			ids = append(ids, id)
		}
	}
	return ids, nil
}
