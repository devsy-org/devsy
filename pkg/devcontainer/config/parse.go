package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/hujson"
)

// DevContainerFeatureFileName is the manifest file name for a devcontainer feature.
const DevContainerFeatureFileName = "devcontainer-feature.json"

// ParseOptions configures devcontainer.json discovery and selection.
type ParseOptions struct {
	// Selector chooses among multiple discovered configs. When nil, the first
	// match is used (and ambiguity is not rejected).
	Selector ConfigSelector

	// ForceSelect skips the conventional root-config short-circuits so the
	// selector always runs against discovered configs. Used when a specific
	// config was explicitly requested, so a root config cannot shadow it.
	ForceSelect bool
}

// ParseDevContainerFeature reads and parses the devcontainer-feature.json in folder.
func ParseDevContainerFeature(folder string) (*FeatureConfig, error) {
	path, err := filepath.Abs(filepath.Join(folder, DevContainerFeatureFileName))
	if err != nil {
		return nil, fmt.Errorf("make path absolute: %w", err)
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("%s is missing in feature folder", DevContainerFeatureFileName)
	}

	data, err := os.ReadFile(path) //nolint:gosec // caller-provided feature folder
	if err != nil {
		return nil, err
	}

	normalized, err := hujson.Standardize(data)
	if err != nil {
		return nil, fmt.Errorf("parse jsonc: %w", err)
	}

	featureConfig := &FeatureConfig{}
	if err := json.Unmarshal(normalized, featureConfig); err != nil {
		return nil, err
	}

	if featureConfig.ID == "" {
		// "id" is required by the devcontainer Feature schema.
		return nil, fmt.Errorf(
			"%s is missing required property \"id\"", DevContainerFeatureFileName,
		)
	}

	featureConfig.Origin = path
	return featureConfig, nil
}

// SaveDevContainerJSON writes config to disk at its Origin.
func SaveDevContainerJSON(config *DevContainerConfig) error {
	if config.Origin == "" {
		return fmt.Errorf("no origin in config")
	}

	// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
	if err := os.MkdirAll(filepath.Dir(config.Origin), 0o755); err != nil {
		return err
	}

	out, err := json.Marshal(config)
	if err != nil {
		return err
	}

	// #nosec G306 -- TODO Consider using a more secure permission setting and ownership if needed.
	return os.WriteFile(config.Origin, out, 0o644)
}

// ParseDevContainerJSONFile parses the devcontainer.json at jsonFilePath,
// resolving `extends` and migrating legacy fields.
func ParseDevContainerJSONFile(
	ctx context.Context,
	jsonFilePath string,
) (*DevContainerConfig, error) {
	path, err := filepath.Abs(jsonFilePath)
	if err != nil {
		return nil, fmt.Errorf("make path absolute: %w", err)
	}

	data, err := os.ReadFile(path) //nolint:gosec // caller-provided workspace folder
	if err != nil {
		return nil, err
	}

	normalized, err := hujson.Standardize(data)
	if err != nil {
		return nil, fmt.Errorf("parse jsonc: %w", err)
	}

	devContainer := &DevContainerConfig{}
	if err := json.Unmarshal(normalized, devContainer); err != nil {
		return nil, err
	}
	devContainer.Origin = path

	devContainer, err = resolveExtends(ctx, devContainer, path)
	if err != nil {
		return nil, err
	}

	return replaceLegacy(devContainer)
}

// resolveExtends resolves any `extends` references, merging parent configs into
// the given config. It is a no-op when the config has no `extends`.
func resolveExtends(
	ctx context.Context,
	devContainer *DevContainerConfig,
	path string,
) (*DevContainerConfig, error) {
	if devContainer.Extends.IsEmpty() {
		return devContainer, nil
	}

	visited := map[string]bool{path: true}
	declaringDir := filepath.Dir(path)

	replacer := extendsVarReplacer(declaringDir)
	for i, ref := range devContainer.Extends {
		devContainer.Extends[i] = ResolveString(ref, replacer)
	}

	parent, err := resolveExtendsArray(ctx, devContainer.Extends, declaringDir, visited)
	if err != nil {
		return nil, err
	}
	return mergeExtendsConfigs(parent, devContainer), nil
}

// extendsVarReplacer returns a ReplaceFunction that resolves only local-scope
// variables, suitable for use before the container exists (during `extends`
// path resolution).
func extendsVarReplacer(localWorkspaceFolder string) ReplaceFunction {
	return func(match, variable string, args []string) string {
		switch variable {
		case varLocalEnv:
			if len(args) == 0 {
				return match
			}
			if val, ok := os.LookupEnv(args[0]); ok {
				return val
			}
			if len(args) > 1 {
				return strings.Join(args[1:], ":")
			}
			return ""
		case varLocalWorkspaceFolder:
			return localWorkspaceFolder
		case "localWorkspaceFolderBasename":
			return filepath.Base(localWorkspaceFolder)
		default:
			return match
		}
	}
}

// ParseDevContainerJSON discovers and parses the devcontainer.json for a folder,
// optionally at relativePath. It returns (nil, nil) when no config is found.
func ParseDevContainerJSON(
	ctx context.Context,
	folder, relativePath string,
) (*DevContainerConfig, error) {
	return ParseDevContainerJSONWithOptions(ctx, folder, relativePath, ParseOptions{})
}

// ParseDevContainerJSONWithOptions discovers and parses the devcontainer.json for
// a folder using the given options to control selection. It returns (nil, nil)
// when no config is found.
func ParseDevContainerJSONWithOptions(
	ctx context.Context,
	folder, relativePath string,
	opts ParseOptions,
) (*DevContainerConfig, error) {
	path, err := resolveDevContainerPath(folder, relativePath, opts.Selector, opts.ForceSelect)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, nil
	}
	return ParseDevContainerJSONFile(ctx, path)
}
