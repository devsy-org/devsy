package devcontainer

import (
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
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

func (r *runner) getRawConfig(options provider2.CLIOptions) (*config.DevContainerConfig, error) {
	if r.WorkspaceConfig.Workspace.DevContainerConfig != nil {
		rawParsedConfig := config.CloneDevContainerConfig(
			r.WorkspaceConfig.Workspace.DevContainerConfig,
		)
		if r.WorkspaceConfig.Workspace.DevContainerPath != "" {
			rawParsedConfig.Origin = path.Join(
				filepath.ToSlash(r.LocalWorkspaceFolder),
				r.WorkspaceConfig.Workspace.DevContainerPath,
			)
		} else {
			rawParsedConfig.Origin = path.Join(
				filepath.ToSlash(r.LocalWorkspaceFolder),
				".devcontainer."+pkgconfig.BinaryName+".json",
			)
		}
		return rawParsedConfig, nil
	} else if r.WorkspaceConfig.Workspace.Source.Container != "" {
		return &config.DevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{
				// Default workspace directory for containers
				// Upon inspecting the container, this would be updated to the correct folder, if found set
				WorkspaceFolder: "/",
			},
			RunningContainer: config.RunningContainer{
				ContainerID: r.WorkspaceConfig.Workspace.Source.Container,
			},
			Origin: "",
		}, nil
	} else if crane.ShouldUse(&options) {
		localWorkspaceFolder, err := crane.PullConfigFromSource(r.WorkspaceConfig, &options)
		if err != nil {
			return nil, err
		}

		return config.ParseDevContainerJSON(
			localWorkspaceFolder,
			r.WorkspaceConfig.Workspace.DevContainerPath,
		)
	}

	localWorkspaceFolder := r.LocalWorkspaceFolder
	// if a subpath is specified, let's move to it

	if r.WorkspaceConfig.Workspace.Source.GitSubPath != "" {
		localWorkspaceFolder = filepath.Join(
			r.LocalWorkspaceFolder,
			r.WorkspaceConfig.Workspace.Source.GitSubPath,
		)
	}

	// parse the devcontainer json
	var rawParsedConfig *config.DevContainerConfig
	var err error

	if options.DevContainerID != "" {
		// Use selector to find specific devcontainer by ID
		rawParsedConfig, err = config.ParseDevContainerJSONWithSelector(
			localWorkspaceFolder,
			r.WorkspaceConfig.Workspace.DevContainerPath,
			func(matches []string) (string, error) {
				for _, match := range matches {
					if filepath.Base(filepath.Dir(match)) == options.DevContainerID {
						return match, nil
					}
				}
				return "", fmt.Errorf("devcontainer with ID %q not found", options.DevContainerID)
			},
		)
	} else {
		rawParsedConfig, err = config.ParseDevContainerJSONWithSelector(
			localWorkspaceFolder,
			r.WorkspaceConfig.Workspace.DevContainerPath,
			func(matches []string) (string, error) {
				if len(matches) > 1 {
					ids, _ := config.ListDevContainerIDs(localWorkspaceFolder)
					return "", fmt.Errorf(
						"multiple devcontainer configurations found. Use --devcontainer-id to select one: %v",
						ids,
					)
				}
				return matches[0], nil
			},
		)
	}

	// We want to fail only in case of real errors, non-existing devcontainer.jon
	// will be gracefully handled by the auto-detection mechanism
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("parsing devcontainer.json: %w", err)
	} else if rawParsedConfig == nil {
		log.Infof("Couldn't find a devcontainer.json")
		return r.getDefaultConfig(options)
	}
	return rawParsedConfig, nil
}

func (r *runner) getDefaultConfig(
	options provider2.CLIOptions,
) (*config.DevContainerConfig, error) {
	defaultConfig := &config.DevContainerConfig{}
	if options.FallbackImage != "" {
		log.Infof("Using fallback image %s", options.FallbackImage)
		defaultConfig.ImageContainer = config.ImageContainer{
			Image: options.FallbackImage,
		}
	} else {
		log.Infof("Try detecting project programming language...")
		defaultConfig = language.DefaultConfig(r.LocalWorkspaceFolder)
	}

	defaultConfig.Origin = path.Join(filepath.ToSlash(r.LocalWorkspaceFolder), ".devcontainer.json")
	err := config.SaveDevContainerJSON(defaultConfig)
	if err != nil {
		return nil, fmt.Errorf("write default devcontainer.json: %w", err)
	}
	return defaultConfig, nil
}

func (r *runner) getSubstitutedConfig(
	options provider2.CLIOptions,
) (*config.SubstitutedConfig, *config.SubstitutionContext, error) {
	rawConfig, err := r.getRawConfig(options)
	if err != nil {
		return nil, nil, err
	}

	return r.substitute(options, rawConfig)
}

func (r *runner) substitute(
	options provider2.CLIOptions,
	rawParsedConfig *config.DevContainerConfig,
) (*config.SubstitutedConfig, *config.SubstitutionContext, error) {
	configFile := rawParsedConfig.Origin

	// get workspace folder within container
	workspaceMount, containerWorkspaceFolder := getWorkspace(
		r.LocalWorkspaceFolder,
		r.WorkspaceConfig.Workspace.ID,
		rawParsedConfig,
	)

	// merge InitEnv into environment for variable substitution
	env := config.ListToObject(os.Environ())
	if len(options.InitEnv) > 0 {
		initEnv := config.ListToObject(options.InitEnv)
		maps.Copy(env, initEnv)
	}

	substitutionContext := &config.SubstitutionContext{
		DevContainerID:           config.DeriveDevContainerID(r.LocalWorkspaceFolder, configFile),
		LocalWorkspaceFolder:     r.LocalWorkspaceFolder,
		ContainerWorkspaceFolder: containerWorkspaceFolder,
		Env:                      env,

		WorkspaceMount: workspaceMount,
	}

	// Substitute applies phase-aware variable scoping per the devcontainer spec
	// (https://containers.dev/implementors/reference/ — "Variables in devcontainer.json"):
	// - All workspace-folder variables (local* and container*) are resolved
	//   host-side, including in containerEnv values, because the host knows
	//   ContainerWorkspaceFolder (it is computed by getWorkspace).
	// - Post-container fields (remoteEnv, lifecycle commands, etc.) likewise
	//   resolve containerWorkspaceFolder and containerWorkspaceFolderBasename.
	// - Only containerEnv references (${containerEnv:VAR}) remain literal here;
	//   they are resolved host-side from the image's inspected env (see
	//   ResolveContainerEnvFromImage) before passing to `docker run -e`, and
	//   for remoteEnv inside the container via SubstituteContainerEnv.
	parsedConfig := &config.DevContainerConfig{}
	err := config.Substitute(substitutionContext, rawParsedConfig, parsedConfig)
	if err != nil {
		return nil, nil, err
	}
	if parsedConfig.WorkspaceFolder != "" &&
		parsedConfig.WorkspaceFolder != substitutionContext.ContainerWorkspaceFolder {
		// WorkspaceFolder was overridden via devcontainer.json. Re-run
		// substitution so containerEnv/remoteEnv values that reference
		// ${containerWorkspaceFolder} pick up the override.
		substitutionContext.ContainerWorkspaceFolder = parsedConfig.WorkspaceFolder
		reSubstituted := &config.DevContainerConfig{}
		if err := config.Substitute(
			substitutionContext,
			rawParsedConfig,
			reSubstituted,
		); err != nil {
			return nil, nil, err
		}
		parsedConfig = reSubstituted
	}
	if parsedConfig.WorkspaceMount != nil {
		substitutionContext.WorkspaceMount = *parsedConfig.WorkspaceMount
	}

	if options.WorkspaceMountConsistency != "" {
		substitutionContext.WorkspaceMount = mountSetConsistency(
			substitutionContext.WorkspaceMount,
			options.WorkspaceMountConsistency,
		)
	}

	// merge additional mounts from CLI --mount flags
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

	// merge additional features from CLI flag
	if options.AdditionalFeatures != "" {
		additionalFeatures := make(map[string]any)
		if err := json.Unmarshal(
			[]byte(options.AdditionalFeatures),
			&additionalFeatures,
		); err != nil {
			return nil, nil, fmt.Errorf("parse --additional-features JSON: %w", err)
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
	}

	parsedConfig.Origin = configFile
	return &config.SubstitutedConfig{
		Config: parsedConfig,
		Raw:    rawParsedConfig,
	}, substitutionContext, nil
}
