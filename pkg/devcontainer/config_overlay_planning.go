package devcontainer

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"strings"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
)

const (
	overlayImageField                       = "image"
	overlayLegacyDockerfileField            = "dockerFile"
	overlayDockerfileField                  = "dockerfile"
	overlayContextField                     = "context"
	overlayBuildField                       = "build"
	overlayDockerComposeFileField           = "dockerComposeFile"
	overlayServiceField                     = "service"
	overlayRunServicesField                 = "runServices"
	overlayInitializeCommandField           = "initializeCommand"
	overlayFeaturesField                    = "features"
	overlayOverrideFeatureInstallOrderField = "overrideFeatureInstallOrder"
	overlayTargetField                      = "target"
	overlayArgsField                        = "args"
	overlayCacheFromField                   = "cacheFrom"
	overlayOptionsField                     = "options"
)

const (
	overlayComposeSelection = "compose"
	overlayDefaultSelection = "default"
)

var overlayStructuralFields = []string{
	overlayImageField,
	overlayLegacyDockerfileField,
	overlayContextField,
	overlayBuildField,
	overlayDockerComposeFileField,
	overlayServiceField,
	overlayRunServicesField,
}

func applyOverlayPlanningInputs(
	base, overlay *config.DevContainerConfig,
	substitution *config.SubstitutionContext,
) error {
	if overlay == nil {
		return nil
	}
	sources := overlay.Sources
	if len(sources) == 0 {
		data, err := json.Marshal(overlay)
		if err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		sources = []config.ConfigSource{{Origin: overlay.Origin, Fields: fields}}
	}
	for _, source := range sources {
		if err := applyOverlaySource(base, source, substitution); err != nil {
			return fmt.Errorf("devcontainer overlay %q: %w", source.Origin, err)
		}
	}
	return validateOverlaySelection(base)
}

func overlaySourceValues(source config.ConfigSource) (map[string]any, error) {
	fields := make(map[string]json.RawMessage)
	keys := append(
		append([]string(nil), overlayStructuralFields...),
		overlayInitializeCommandField,
		overlayFeaturesField,
		overlayOverrideFeatureInstallOrderField,
	)
	for _, key := range keys {
		if value, ok := source.Fields[key]; ok {
			if strings.TrimSpace(string(value)) == "null" {
				if overlayNullPlanningField(key) {
					continue
				}
				return nil, fmt.Errorf("%s cannot be null", key)
			}
			fields[key] = value
		}
	}
	var values map[string]any
	dataFields, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(dataFields, &values); err != nil {
		return nil, err
	}
	if err := validateOverlayStructuralNulls(values); err != nil {
		return nil, err
	}
	return values, nil
}

func validateOverlayStructuralNulls(values map[string]any) error {
	for _, field := range overlayStructuralFields {
		if value, present := values[field]; present {
			if err := validateOverlayNonNullValue(value, field); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateOverlayNonNullValue(value any, field string) error {
	switch typed := value.(type) {
	case nil:
		return fmt.Errorf("%s cannot be null", field)
	case map[string]any:
		for key, child := range typed {
			if err := validateOverlayNonNullValue(child, field+"."+key); err != nil {
				return err
			}
		}
	case []any:
		for i, child := range typed {
			if err := validateOverlayNonNullValue(
				child,
				fmt.Sprintf("%s[%d]", field, i),
			); err != nil {
				return err
			}
		}
	}
	return nil
}

// Nil feature planning fields retain their pre-existing no-override semantics.
func overlayNullPlanningField(key string) bool {
	return key == overlayFeaturesField || key == overlayOverrideFeatureInstallOrderField
}

func applyOverlaySource(
	base *config.DevContainerConfig,
	source config.ConfigSource,
	substitution *config.SubstitutionContext,
) error {
	values, err := overlaySourceValues(source)
	if err != nil {
		return err
	}
	if err := normalizeOverlayPaths(values, base.Origin, source.Origin, substitution); err != nil {
		return err
	}
	data, err := json.Marshal(values)
	if err != nil {
		return err
	}
	var projected config.DevContainerConfig
	if err := json.Unmarshal(data, &projected); err != nil {
		return err
	}
	projected.Origin = base.Origin
	// Paths are already in the primary coordinate system.
	if err := applyOverlayBuildInputs(base, &projected); err != nil {
		return err
	}
	applyOverlayInitializeCommand(base, &projected, values)
	return mergeOverlayStructure(base, &projected, values)
}

func applyOverlayInitializeCommand(
	base, projected *config.DevContainerConfig,
	values map[string]any,
) {
	value, present := values[overlayInitializeCommandField]
	if !present {
		return
	}
	base.InitializeCommand = projected.InitializeCommand
	switch v := value.(type) {
	case string:
		if v == "" {
			base.InitializeCommand = nil
		}
	case []any:
		if len(v) == 0 {
			base.InitializeCommand = nil
		}
	case map[string]any:
		if len(v) == 0 {
			base.InitializeCommand = nil
		}
	}
}

func normalizeOverlayPaths(
	values map[string]any,
	baseOrigin, sourceOrigin string,
	substitution *config.SubstitutionContext,
) error {
	steps := []func(map[string]any, string, string, *config.SubstitutionContext) error{
		normalizeOverlayAssetPaths, normalizeOverlayBuildPaths, normalizeOverlayComposePaths,
		normalizeOverlayFeatures, normalizeOverlayFeatureOrder,
	}
	for _, step := range steps {
		if err := step(values, baseOrigin, sourceOrigin, substitution); err != nil {
			return err
		}
	}
	return nil
}

func normalizeOverlayAssetPaths(
	values map[string]any,
	baseOrigin, sourceOrigin string,
	substitution *config.SubstitutionContext,
) error {
	for _, key := range []string{overlayLegacyDockerfileField, overlayContextField} {
		if value, ok := values[key]; ok {
			path, err := substituteOverlayPathValue(value, substitution)
			if err != nil {
				return err
			}
			normalized, err := overlayAssetPath(path, baseOrigin, sourceOrigin, key)
			if err != nil {
				return err
			}
			values[key] = normalized
		}
	}
	return nil
}

func normalizeOverlayBuildPaths(
	values map[string]any,
	baseOrigin, sourceOrigin string,
	substitution *config.SubstitutionContext,
) error {
	build, ok := values[overlayBuildField].(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{overlayDockerfileField, overlayContextField} {
		if value, present := build[key]; present {
			path, err := substituteOverlayPathValue(value, substitution)
			if err != nil {
				return err
			}
			normalized, err := overlayAssetPath(path, baseOrigin, sourceOrigin, "build."+key)
			if err != nil {
				return err
			}
			build[key] = normalized
		}
	}
	return nil
}

func normalizeOverlayComposePaths(
	values map[string]any,
	baseOrigin, sourceOrigin string,
	substitution *config.SubstitutionContext,
) error {
	paths, ok := values[overlayDockerComposeFileField]
	if !ok {
		return nil
	}
	list, err := overlayComposePathList(paths)
	if err != nil {
		return err
	}
	for i, value := range list {
		path, err := substituteOverlayPathValue(value, substitution)
		if err != nil {
			return err
		}
		normalized, err := overlayAssetPath(
			path,
			baseOrigin,
			sourceOrigin,
			overlayDockerComposeFileField,
		)
		if err != nil {
			return err
		}
		list[i] = normalized
	}
	values[overlayDockerComposeFileField] = list
	return nil
}

func overlayComposePathList(paths any) ([]any, error) {
	var list []any
	switch value := paths.(type) {
	case string:
		list = []any{value}
	case []any:
		list = value
	default:
		return nil, fmt.Errorf("dockerComposeFile must be a path or path array")
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("dockerComposeFile cannot be empty")
	}
	return list, nil
}

func normalizeOverlayFeatures(
	values map[string]any,
	baseOrigin, sourceOrigin string,
	substitution *config.SubstitutionContext,
) error {
	features, ok := values[overlayFeaturesField].(map[string]any)
	if !ok {
		return nil
	}
	rebased := make(map[string]any, len(features))
	for id, value := range features {
		resolved, err := substituteOverlayPath(id, substitution)
		if err != nil {
			return err
		}
		normalized, err := overlayFeaturePath(resolved, baseOrigin, sourceOrigin)
		if err != nil {
			return err
		}
		rebased[normalized] = value
	}
	values[overlayFeaturesField] = rebased
	return nil
}

func normalizeOverlayFeatureOrder(
	values map[string]any,
	baseOrigin, sourceOrigin string,
	substitution *config.SubstitutionContext,
) error {
	order, ok := values[overlayOverrideFeatureInstallOrderField].([]any)
	if !ok {
		return nil
	}
	for i, value := range order {
		resolved, err := substituteOverlayPathValue(value, substitution)
		if err != nil {
			return err
		}
		normalized, err := overlayFeaturePath(resolved, baseOrigin, sourceOrigin)
		if err != nil {
			return err
		}
		order[i] = normalized
	}
	return nil
}

func substituteOverlayPathValue(
	value any,
	substitution *config.SubstitutionContext,
) (string, error) {
	path, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("overlay path must be a string")
	}
	return substituteOverlayPath(path, substitution)
}

func substituteOverlayPath(path string, substitution *config.SubstitutionContext) (string, error) {
	out := map[string]string{}
	if err := config.Substitute(substitution, map[string]string{"path": path}, &out); err != nil {
		return "", err
	}
	return out["path"], nil
}

func overlayFeaturePath(id, baseOrigin, sourceOrigin string) (string, error) {
	if !strings.HasPrefix(id, "./") && !strings.HasPrefix(id, "../") {
		return id, nil
	}
	if strings.HasPrefix(sourceOrigin, "oci://") {
		return "", fmt.Errorf("local feature %q requires a local declaring file", id)
	}
	return rebaseOverlayLocalFeatureID(baseOrigin, sourceOrigin, id)
}

func overlayAssetPath(value any, baseOrigin, sourceOrigin, field string) (string, error) {
	path, ok := value.(string)
	if !ok || strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%s must be a non-empty path", field)
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	if strings.HasPrefix(sourceOrigin, "oci://") {
		return "", fmt.Errorf("%s relative path requires a local declaring file", field)
	}
	absolute := filepath.Join(filepath.Dir(sourceOrigin), filepath.FromSlash(path))
	relative, err := filepath.Rel(filepath.Dir(baseOrigin), absolute)
	if err != nil {
		// Structural assets accept absolute paths across Windows volumes;
		// local features still require a relative ID and retain their error.
		if filepath.IsAbs(absolute) {
			return filepath.Clean(absolute), nil
		}
		return "", fmt.Errorf("resolve %s from %q: %w", field, sourceOrigin, err)
	}
	return filepath.ToSlash(relative), nil
}

func mergeOverlayStructure(base, overlay *config.DevContainerConfig, fields map[string]any) error {
	if err := validateOverlayStructuralFields(fields); err != nil {
		return err
	}
	if err := validateOverlayAliases(overlay); err != nil {
		return err
	}
	selected, err := overlaySelectedKind(overlay, fields)
	if err != nil {
		return err
	}
	if err := prepareOverlaySelection(base, selected); err != nil {
		return err
	}
	mergeOverlaySelectors(base, overlay, selected, fields)
	mergeOverlayDockerfilePaths(base, overlay)
	mergeOverlayBuildProperties(base, overlay, fields)
	return nil
}

func mergeOverlaySelectors(
	base, overlay *config.DevContainerConfig,
	selected string,
	fields map[string]any,
) {
	switch selected {
	case overlayImageField:
		base.Image = overlay.Image
	case overlayComposeSelection:
		base.DockerComposeFile = overlay.DockerComposeFile
	}
	if _, ok := fields[overlayServiceField]; ok {
		base.Service = overlay.Service
	}
	if _, ok := fields[overlayRunServicesField]; ok {
		base.RunServices = overlay.RunServices
	}
}

func validateOverlayStructuralFields(fields map[string]any) error {
	for _, key := range []string{overlayImageField, overlayServiceField} {
		if value, present := fields[key]; present {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return fmt.Errorf("%s cannot be empty", key)
			}
		}
	}
	return validateOverlayBuildFields(fields)
}

func validateOverlayBuildFields(fields map[string]any) error {
	value, present := fields[overlayBuildField]
	if !present {
		return nil
	}
	build, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("build must be an object")
	}
	for _, key := range []string{
		overlayDockerfileField, overlayContextField, overlayTargetField,
		overlayArgsField, overlayCacheFromField, overlayOptionsField,
	} {
		if value, present := build[key]; present && value == nil {
			return fmt.Errorf("build.%s cannot be null", key)
		}
	}
	return nil
}

func validateOverlayAliases(overlay *config.DevContainerConfig) error {
	if overlay.Build == nil {
		return nil
	}
	if overlay.Dockerfile != "" && overlay.Build.Dockerfile != "" &&
		overlay.Dockerfile != overlay.Build.Dockerfile {
		return fmt.Errorf("dockerFile conflicts with build.dockerfile")
	}
	if overlay.Context != "" && overlay.Build.Context != "" &&
		overlay.Context != overlay.Build.Context {
		return fmt.Errorf("context conflicts with build.context")
	}
	return nil
}

func overlaySelectedKind(
	overlay *config.DevContainerConfig,
	fields map[string]any,
) (string, error) {
	selections := []string{}
	if _, ok := fields[overlayImageField]; ok {
		selections = append(selections, overlayImageField)
	}
	if overlay.GetDockerfile() != "" {
		selections = append(selections, overlayDockerfileField)
	}
	if _, ok := fields[overlayDockerComposeFileField]; ok {
		selections = append(selections, overlayComposeSelection)
	}
	if len(selections) > 1 {
		return "", fmt.Errorf("overlay declares multiple build types")
	}
	if len(selections) == 0 {
		return "", nil
	}
	return selections[0], nil
}

func prepareOverlaySelection(base *config.DevContainerConfig, selected string) error {
	if selected == "" {
		return nil
	}
	if base.ContainerID != "" {
		return fmt.Errorf("structural overlay cannot replace an attached containerID")
	}
	if selected != overlayConfigKind(base) {
		base.ImageContainer = config.ImageContainer{}
		base.DockerfileContainer = config.DockerfileContainer{}
		base.ComposeContainer = config.ComposeContainer{}
	}
	switch selected {
	case overlayImageField:
		base.DockerfileContainer = config.DockerfileContainer{}
		base.ComposeContainer = config.ComposeContainer{}
	case overlayDockerfileField:
		base.ImageContainer = config.ImageContainer{}
		base.ComposeContainer = config.ComposeContainer{}
	case overlayComposeSelection:
		base.ImageContainer = config.ImageContainer{}
		base.DockerfileContainer = config.DockerfileContainer{}
	}
	return nil
}

func mergeOverlayDockerfilePaths(base, overlay *config.DevContainerConfig) {
	if overlay.GetDockerfile() != "" {
		base.Dockerfile = overlay.GetDockerfile()
		if base.Build != nil {
			base.Build.Dockerfile = ""
		}
	}
	if overlay.GetContext() != "" {
		base.Context = overlay.GetContext()
		if base.Build != nil {
			base.Build.Context = ""
		}
	}
}

func mergeOverlayBuildProperties(base, overlay *config.DevContainerConfig, fields map[string]any) {
	buildFields, ok := fields[overlayBuildField].(map[string]any)
	if !ok {
		return
	}
	if base.Build == nil {
		base.Build = &config.ConfigBuildOptions{}
	}
	mergeOverlayBuildArguments(base.Build, overlay.Build, buildFields)
	if _, ok := buildFields[overlayTargetField]; ok {
		base.Build.Target = overlay.Build.Target
	}
	if _, ok := buildFields[overlayCacheFromField]; ok {
		base.Build.CacheFrom = overlay.Build.CacheFrom
	}
	if _, ok := buildFields[overlayOptionsField]; ok {
		base.Build.Options = overlay.Build.Options
	}
}

func mergeOverlayBuildArguments(base, overlay *config.ConfigBuildOptions, fields map[string]any) {
	if _, ok := fields[overlayArgsField]; !ok {
		return
	}
	if base.Args == nil {
		base.Args = make(map[string]string)
	}
	maps.Copy(base.Args, overlay.Args)
}

func overlayConfigKind(c *config.DevContainerConfig) string {
	switch {
	case c.GetDockerfile() != "":
		return overlayDockerfileField
	case c.Image != "":
		return overlayImageField
	case len(c.DockerComposeFile) > 0:
		return overlayComposeSelection
	case c.ContainerID != "":
		return "attached"
	default:
		return overlayDefaultSelection
	}
}

func validateOverlaySelection(c *config.DevContainerConfig) error {
	if c.Build != nil || c.Context != "" {
		if c.GetDockerfile() == "" {
			return fmt.Errorf("overlay build properties require a Dockerfile selection")
		}
	}
	return validateOverlayComposeSelection(c)
}

func validateOverlayComposeSelection(c *config.DevContainerConfig) error {
	if len(c.DockerComposeFile) > 0 && c.Service == "" {
		return fmt.Errorf("overlay Compose selection requires service")
	}
	if len(c.DockerComposeFile) == 0 && (c.Service != "" || c.RunServices != nil) {
		return fmt.Errorf("overlay service and runServices require Compose selection")
	}
	return nil
}
