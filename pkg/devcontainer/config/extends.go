package config

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"github.com/tailscale/hujson"
)

// ExtendsRef holds one or more paths to parent devcontainer.json files.
// JSON accepts either a single string or an array of strings.
type ExtendsRef []string

func (e ExtendsRef) IsEmpty() bool {
	return len(e) == 0
}

func (e ExtendsRef) MarshalJSON() ([]byte, error) {
	if len(e) == 1 {
		return json.Marshal(e[0])
	}
	return json.Marshal([]string(e))
}

func (e *ExtendsRef) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*e = ExtendsRef{s}
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		*e = ExtendsRef(arr)
		return nil
	}
	return fmt.Errorf("extends: must be a string or array of strings")
}

// resolveExtendsArray resolves multiple extends refs left-to-right,
// merging each on top of the previous result. The final merged config
// is returned as the combined parent for the declaring file.
func resolveExtendsArray(
	ctx context.Context,
	refs ExtendsRef, declaringDir string,
	visited map[string]bool,
) (*DevContainerConfig, error) {
	var merged *DevContainerConfig
	for _, ref := range refs {
		resolved, err := resolveExtendsSingle(ctx, ref, declaringDir, visited)
		if err != nil {
			return nil, err
		}
		if merged == nil {
			merged = resolved
		} else {
			merged = mergeExtendsConfigs(merged, resolved)
		}
	}
	return merged, nil
}

// resolveExtendsSingle resolves a single extends reference.
// It dispatches to OCI resolution for registry refs or local file resolution otherwise.
func resolveExtendsSingle(
	ctx context.Context,
	extendsRef, declaringDir string,
	visited map[string]bool,
) (*DevContainerConfig, error) {
	if isOCIRef(extendsRef) {
		return resolveOCIExtends(ctx, extendsRef, visited)
	}

	refPath := extendsRef
	if !filepath.IsAbs(refPath) {
		refPath = filepath.Join(declaringDir, refPath)
	}

	absPath, err := filepath.Abs(refPath)
	if err != nil {
		return nil, fmt.Errorf("extends: resolve path %q: %w", extendsRef, err)
	}

	if visited[absPath] {
		return nil, fmt.Errorf("extends: cycle detected, %q already in chain", absPath)
	}

	return parseDevContainerJSONFileWithVisited(ctx, absPath, visited)
}

// parseDevContainerJSONFileWithVisited parses a devcontainer.json and recursively resolves extends.
func parseDevContainerJSONFileWithVisited(
	ctx context.Context,
	path string,
	visited map[string]bool,
) (*DevContainerConfig, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("make path absolute: %w", err)
	}

	// Mark this file as visited
	visited[absPath] = true

	// #nosec G304 -- path is derived from user-authored devcontainer.json extends field
	bytes, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("extends: read %q: %w", absPath, err)
	}

	devContainer := &DevContainerConfig{}
	normalized, err := hujson.Standardize(bytes)
	if err != nil {
		return nil, fmt.Errorf("extends: parse jsonc %q: %w", absPath, err)
	}
	err = json.Unmarshal(normalized, devContainer)
	if err != nil {
		return nil, fmt.Errorf("extends: unmarshal %q: %w", absPath, err)
	}
	devContainer.Origin = absPath

	// Recursively resolve extends
	if !devContainer.Extends.IsEmpty() {
		declaringDir := filepath.Dir(absPath)
		parent, err := resolveExtendsArray(ctx, devContainer.Extends, declaringDir, visited)
		if err != nil {
			return nil, err
		}
		devContainer = mergeExtendsConfigs(parent, devContainer)
	}

	return devContainer, nil
}

func mergeExtendsConfigs(parent, child *DevContainerConfig) *DevContainerConfig {
	result := CloneDevContainerConfig(parent)

	mergeScalars(result, child)
	mergePointerScalars(result, child)
	mergeMapsInto(result, child)
	mergeArrays(result, child)
	mergeLifecycleHooks(result, child)
	mergeNestedStructs(result, child)

	// Special
	result.Origin = child.Origin
	result.Extends = nil

	return result
}

func mergeScalars(result, child *DevContainerConfig) {
	mergeBaseScalars(result, child)
	mergeContainerScalars(result, child)
}

func mergeBaseScalars(result, child *DevContainerConfig) {
	if child.Name != "" {
		result.Name = child.Name
	}
	if child.Image != "" {
		result.Image = child.Image
	}
	if child.Dockerfile != "" {
		result.Dockerfile = child.Dockerfile
	}
	if child.Context != "" {
		result.Context = child.Context
	}
	if child.Service != "" {
		result.Service = child.Service
	}
	if child.ContainerUser != "" {
		result.ContainerUser = child.ContainerUser
	}
	if child.RemoteUser != "" {
		result.RemoteUser = child.RemoteUser
	}
}

func mergeContainerScalars(result, child *DevContainerConfig) {
	if child.WorkspaceFolder != "" {
		result.WorkspaceFolder = child.WorkspaceFolder
	}
	if child.WorkspaceMount != nil {
		result.WorkspaceMount = child.WorkspaceMount
	}
	if child.ShutdownAction != "" {
		result.ShutdownAction = child.ShutdownAction
	}
	if child.WaitFor != "" {
		result.WaitFor = child.WaitFor
	}
	if child.UserEnvProbe != "" {
		result.UserEnvProbe = child.UserEnvProbe
	}
	if child.ContainerID != "" {
		result.ContainerID = child.ContainerID
	}
}

// mergePointerScalars copies non-nil pointer fields from child into result.
func mergePointerScalars(result, child *DevContainerConfig) {
	if child.UpdateRemoteUserUID != nil {
		result.UpdateRemoteUserUID = child.UpdateRemoteUserUID
	}
	if child.OverrideCommand != nil {
		result.OverrideCommand = child.OverrideCommand
	}
	if child.Init != nil {
		result.Init = child.Init
	}
	if child.Privileged != nil {
		result.Privileged = child.Privileged
	}
	if child.OtherPortsAttributes != nil {
		result.OtherPortsAttributes = child.OtherPortsAttributes
	}
}

// mergeMapsInto deep-merges map fields (parent as base, child keys override).
func mergeMapsInto(result, child *DevContainerConfig) {
	result.Features = deepMergeMapAny(result.Features, child.Features)
	result.PortsAttributes = deepMergeMap(result.PortsAttributes, child.PortsAttributes)
	result.RemoteEnv = deepMergeMap(result.RemoteEnv, child.RemoteEnv)
	result.ContainerEnv = deepMergeMap(result.ContainerEnv, child.ContainerEnv)
	result.Customizations = deepMergeMapAny(result.Customizations, child.Customizations)
	result.Secrets = deepMergeMap(result.Secrets, child.Secrets)
}

func mergeArrays(result, child *DevContainerConfig) {
	mergeNonComposeArrays(result, child)
	mergeComposeArrays(result, child)
}

func mergeNonComposeArrays(result, child *DevContainerConfig) {
	if child.ForwardPorts != nil {
		result.ForwardPorts = child.ForwardPorts
	}
	if child.Mounts != nil {
		result.Mounts = child.Mounts
	}
	if child.RunArgs != nil {
		result.RunArgs = child.RunArgs
	}
	if child.CapAdd != nil {
		result.CapAdd = child.CapAdd
	}
	if child.SecurityOpt != nil {
		result.SecurityOpt = child.SecurityOpt
	}
	if child.AppPort != nil {
		result.AppPort = child.AppPort
	}
}

func mergeComposeArrays(result, child *DevContainerConfig) {
	if child.RunServices != nil {
		result.RunServices = child.RunServices
	}
	if child.OverrideFeatureInstallOrder != nil {
		result.OverrideFeatureInstallOrder = child.OverrideFeatureInstallOrder
	}
	if child.DockerComposeFile != nil {
		result.DockerComposeFile = child.DockerComposeFile
	}
}

// mergeLifecycleHooks replaces lifecycle hooks when child has non-empty values.
func mergeLifecycleHooks(result, child *DevContainerConfig) {
	if len(child.InitializeCommand) > 0 {
		result.InitializeCommand = child.InitializeCommand
	}
	if len(child.OnCreateCommand) > 0 {
		result.OnCreateCommand = child.OnCreateCommand
	}
	if len(child.UpdateContentCommand) > 0 {
		result.UpdateContentCommand = child.UpdateContentCommand
	}
	if len(child.PostCreateCommand) > 0 {
		result.PostCreateCommand = child.PostCreateCommand
	}
	if len(child.PostStartCommand) > 0 {
		result.PostStartCommand = child.PostStartCommand
	}
	if len(child.PostAttachCommand) > 0 {
		result.PostAttachCommand = child.PostAttachCommand
	}
}

// mergeNestedStructs handles Build and HostRequirements merging.
func mergeNestedStructs(result, child *DevContainerConfig) {
	if child.Build != nil {
		if result.Build != nil {
			// Deep merge Build.Args
			mergedArgs := deepMergeMap(result.Build.Args, child.Build.Args)
			result.Build = child.Build
			if mergedArgs != nil {
				result.Build.Args = mergedArgs
			}
		} else {
			result.Build = child.Build
		}
	}

	if child.HostRequirements != nil {
		result.HostRequirements = child.HostRequirements
	}
}

// deepMergeMap merges two maps where child keys override parent keys.
func deepMergeMap[V any](parent, child map[string]V) map[string]V {
	if parent == nil && child == nil {
		return nil
	}
	merged := make(map[string]V)
	maps.Copy(merged, parent)
	maps.Copy(merged, child)
	return merged
}

// deepMergeMapAny merges two map[string]any maps where child keys override parent keys.
func deepMergeMapAny(parent, child map[string]any) map[string]any {
	if parent == nil && child == nil {
		return nil
	}
	merged := make(map[string]any)
	maps.Copy(merged, parent)
	maps.Copy(merged, child)
	return merged
}
