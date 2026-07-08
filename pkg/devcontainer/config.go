package devcontainer

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/crane"
	"github.com/devsy-org/devsy/pkg/language"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
)

// getRawConfig resolves the raw devcontainer config for the workspace, trying
// each supported source in order and falling back to an auto-detected default
// when none applies.
func (r *runner) getRawConfig(options provider.CLIOptions) (*config.DevContainerConfig, error) {
	if conf := r.rawConfigFromWorkspace(); conf != nil {
		return conf, nil
	}
	if conf := r.rawConfigFromContainer(); conf != nil {
		return conf, nil
	}
	if crane.ShouldUse(&options) {
		return r.rawConfigFromCrane(options)
	}
	return r.rawConfigFromFilesystem(options)
}

// rawConfigFromWorkspace returns the config embedded in the workspace metadata,
// or nil when none is present.
func (r *runner) rawConfigFromWorkspace() *config.DevContainerConfig {
	if r.workspaceConfig.Workspace.DevContainerConfig == nil {
		return nil
	}

	rawConfig := config.CloneDevContainerConfig(r.workspaceConfig.Workspace.DevContainerConfig)
	if devContainerPath := r.workspaceConfig.Workspace.DevContainerPath; devContainerPath != "" {
		rawConfig.Origin = path.Join(filepath.ToSlash(r.localWorkspaceFolder), devContainerPath)
	} else {
		rawConfig.Origin = path.Join(
			filepath.ToSlash(r.localWorkspaceFolder),
			".devcontainer."+pkgconfig.BinaryName+".json",
		)
	}
	return rawConfig
}

// rawConfigFromContainer returns a synthetic config for a running-container
// source, or nil when the source is not a container.
func (r *runner) rawConfigFromContainer() *config.DevContainerConfig {
	containerID := r.workspaceConfig.Workspace.Source.Container
	if containerID == "" {
		return nil
	}

	return &config.DevContainerConfig{
		DevContainerConfigBase: config.DevContainerConfigBase{
			// Default workspace directory for containers. Once the container is
			// inspected this is updated to the discovered folder, if any.
			WorkspaceFolder: "/",
		},
		RunningContainer: config.RunningContainer{ContainerID: containerID},
	}
}

// rawConfigFromCrane pulls the config from the image source via crane.
func (r *runner) rawConfigFromCrane(
	options provider.CLIOptions,
) (*config.DevContainerConfig, error) {
	localWorkspaceFolder, err := crane.PullConfigFromSource(r.workspaceConfig, &options)
	if err != nil {
		return nil, err
	}
	return config.ParseDevContainerJSON(
		context.Background(),
		localWorkspaceFolder,
		r.workspaceConfig.Workspace.DevContainerPath,
	)
}

// rawConfigFromFilesystem discovers and parses the devcontainer.json under the
// workspace folder, falling back to an auto-detected default when none is found.
func (r *runner) rawConfigFromFilesystem(
	options provider.CLIOptions,
) (*config.DevContainerConfig, error) {
	localWorkspaceFolder := r.localWorkspaceFolder
	if subPath := r.workspaceConfig.Workspace.Source.GitSubPath; subPath != "" {
		localWorkspaceFolder = filepath.Join(localWorkspaceFolder, subPath)
	}

	opts := config.ParseOptions{Selector: config.SelectSingle(localWorkspaceFolder)}
	if options.DevContainerID != "" {
		// An explicit id must not be shadowed by a root config, and a mismatch
		// must error rather than silently fall back.
		opts = config.ParseOptions{
			Selector:    config.SelectByID(options.DevContainerID),
			ForceSelect: true,
		}
	}

	rawConfig, err := config.ParseDevContainerJSONWithOptions(
		context.Background(),
		localWorkspaceFolder,
		r.workspaceConfig.Workspace.DevContainerPath,
		opts,
	)
	// A missing devcontainer.json is not an error: fall back to auto-detection.
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("parsing devcontainer.json: %w", err)
	}
	if rawConfig == nil {
		log.Infof("Couldn't find a devcontainer.json")
		return r.getDefaultConfig(options)
	}

	if msg := workspaceMountFolderWarning(rawConfig); msg != "" {
		log.Warnf("%s", msg)
	}
	return rawConfig, nil
}

// workspaceMountFolderWarning returns a warning when only one of
// workspaceMount / workspaceFolder is set, and "" when the pairing is fine.
// Per the devcontainer spec both must be set together
// (https://containers.dev/implementors/json_reference/). devsy still applies a
// sensible default for the missing one, so this is a warning, not an error.
func workspaceMountFolderWarning(conf *config.DevContainerConfig) string {
	if conf == nil {
		return ""
	}
	// A present workspaceMount satisfies the pairing even when it is the empty
	// string, which is devsy's documented "suppress the default mount" signal.
	hasMount := conf.WorkspaceMount != nil
	hasFolder := conf.WorkspaceFolder != ""
	switch {
	case hasMount && !hasFolder:
		return "devcontainer.json sets workspaceMount without workspaceFolder; " +
			"the spec requires both. Falling back to the default workspace folder."
	case hasFolder && !hasMount:
		return "devcontainer.json sets workspaceFolder without workspaceMount; " +
			"the spec requires both. Falling back to the default workspace mount."
	}
	return ""
}

func (r *runner) getDefaultConfig(
	options provider.CLIOptions,
) (*config.DevContainerConfig, error) {
	defaultConfig := &config.DevContainerConfig{}
	if options.FallbackImage != "" {
		log.Infof("Using fallback image %s", options.FallbackImage)
		defaultConfig.ImageContainer = config.ImageContainer{Image: options.FallbackImage}
	} else {
		log.Infof("Try detecting project programming language")
		defaultConfig = language.DefaultConfig(r.localWorkspaceFolder)
	}

	defaultConfig.Origin = path.Join(filepath.ToSlash(r.localWorkspaceFolder), ".devcontainer.json")
	if err := config.SaveDevContainerJSON(defaultConfig); err != nil {
		return nil, fmt.Errorf("write default devcontainer.json: %w", err)
	}
	return defaultConfig, nil
}

func (r *runner) getSubstitutedConfig(
	options provider.CLIOptions,
) (*config.SubstitutedConfig, *config.SubstitutionContext, error) {
	rawConfig, err := r.getRawConfig(options)
	if err != nil {
		return nil, nil, err
	}
	return r.substitute(options, rawConfig)
}

// substitute resolves devcontainer.json variables and applies CLI overrides,
// returning the substituted config alongside the substitution context used.
func (r *runner) substitute(
	options provider.CLIOptions,
	rawParsedConfig *config.DevContainerConfig,
) (*config.SubstitutedConfig, *config.SubstitutionContext, error) {
	substitutionContext := r.buildSubstitutionContext(options, rawParsedConfig)

	parsedConfig, err := applySubstitution(substitutionContext, rawParsedConfig)
	if err != nil {
		return nil, nil, err
	}

	applyMountContext(substitutionContext, parsedConfig, options)

	if err := applyCLIOverrides(parsedConfig, options); err != nil {
		return nil, nil, err
	}

	parsedConfig.Origin = rawParsedConfig.Origin
	return &config.SubstitutedConfig{
		Config: parsedConfig,
		Raw:    rawParsedConfig,
	}, substitutionContext, nil
}

// buildSubstitutionContext assembles the context used for variable substitution:
// the derived container id, workspace folders, and the merged environment.
func (r *runner) buildSubstitutionContext(
	options provider.CLIOptions,
	rawParsedConfig *config.DevContainerConfig,
) *config.SubstitutionContext {
	configFile := rawParsedConfig.Origin
	workspaceMount, containerWorkspaceFolder := getWorkspace(
		r.localWorkspaceFolder,
		r.workspaceConfig.Workspace.ID,
		rawParsedConfig,
	)

	env := config.ListToObject(os.Environ())
	if len(options.InitEnv) > 0 {
		maps.Copy(env, config.ListToObject(options.InitEnv))
	}

	return &config.SubstitutionContext{
		DevContainerID:           config.DeriveDevContainerID(r.localWorkspaceFolder, configFile),
		LocalWorkspaceFolder:     r.localWorkspaceFolder,
		ContainerWorkspaceFolder: containerWorkspaceFolder,
		Env:                      env,
		WorkspaceMount:           workspaceMount,
	}
}

// applySubstitution runs variable substitution over the raw config. When the
// config overrides the container workspace folder, substitution is re-run so
// dependent values pick up the override.
//
// Substitution is phase-aware per the devcontainer spec
// (https://containers.dev/implementors/reference/): workspace-folder variables
// are resolved host-side, while container-env references are resolved later
// once the image environment is known.
func applySubstitution(
	substitutionContext *config.SubstitutionContext,
	rawParsedConfig *config.DevContainerConfig,
) (*config.DevContainerConfig, error) {
	parsedConfig := &config.DevContainerConfig{}
	if err := config.Substitute(substitutionContext, rawParsedConfig, parsedConfig); err != nil {
		return nil, err
	}

	overridesWorkspaceFolder := parsedConfig.WorkspaceFolder != "" &&
		parsedConfig.WorkspaceFolder != substitutionContext.ContainerWorkspaceFolder
	if !overridesWorkspaceFolder {
		return parsedConfig, nil
	}

	substitutionContext.ContainerWorkspaceFolder = parsedConfig.WorkspaceFolder
	reSubstituted := &config.DevContainerConfig{}
	if err := config.Substitute(substitutionContext, rawParsedConfig, reSubstituted); err != nil {
		return nil, err
	}
	return reSubstituted, nil
}

// applyMountContext finalizes the workspace mount on the substitution context
// from the parsed config and the CLI consistency flag.
func applyMountContext(
	substitutionContext *config.SubstitutionContext,
	parsedConfig *config.DevContainerConfig,
	options provider.CLIOptions,
) {
	if parsedConfig.WorkspaceMount != nil {
		substitutionContext.WorkspaceMount = *parsedConfig.WorkspaceMount
	}
	if options.WorkspaceMountConsistency != "" {
		substitutionContext.WorkspaceMount = mountSetConsistency(
			substitutionContext.WorkspaceMount,
			options.WorkspaceMountConsistency,
		)
	}
}

// applyCLIOverrides applies CLI-provided overrides onto the parsed config.
func applyCLIOverrides(
	parsedConfig *config.DevContainerConfig,
	options provider.CLIOptions,
) error {
	for _, mountStr := range options.Mounts {
		m := config.ParseMount(mountStr)
		parsedConfig.Mounts = append(parsedConfig.Mounts, &m)
	}

	if options.DevContainerImage != "" {
		parsedConfig.Build = nil
		parsedConfig.Dockerfile = ""
		parsedConfig.DockerfileContainer = config.DockerfileContainer{}
		parsedConfig.ImageContainer = config.ImageContainer{Image: options.DevContainerImage}
	}

	return mergeAdditionalFeatures(parsedConfig, options.AdditionalFeatures)
}

// mergeAdditionalFeatures merges extra features (a JSON object) into the parsed
// config. It is a no-op when raw is empty.
func mergeAdditionalFeatures(parsedConfig *config.DevContainerConfig, raw string) error {
	if raw == "" {
		return nil
	}

	additionalFeatures := make(map[string]any)
	if err := json.Unmarshal([]byte(raw), &additionalFeatures); err != nil {
		return fmt.Errorf("parse --additional-features JSON: %w", err)
	}

	if parsedConfig.Features == nil {
		parsedConfig.Features = make(map[string]any)
	}
	maps.Copy(parsedConfig.Features, additionalFeatures)
	log.Infof(
		"Merged %d additional feature(s): %v",
		len(additionalFeatures),
		slices.Collect(maps.Keys(additionalFeatures)),
	)
	return nil
}
