package devcontainer

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/devsy-org/devsy/pkg/compose"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/metadata"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/joho/godotenv"
)

const (
	ConfigFilesLabel                = "com.docker.compose.project.config_files"
	FeaturesBuildOverrideFilePrefix = "docker-compose.devcontainer.build"
	FeaturesStartOverrideFilePrefix = "docker-compose.devcontainer.containerFeatures"

	containerStatusRunning = "running"
	composeProjectNameFlag = "--project-name"
)

type composeProjectFiles struct {
	composeFiles      []string
	envFiles          []string
	composeGlobalArgs []string
}

type composeBuildInfo struct {
	imageBuildInfo     *config.ImageBuildInfo
	dockerfileContents string
	buildTarget        string
}

type composeExtendResult struct {
	buildImageName       string
	composeBuildFilePath string
	imageMetadata        *config.ImageMetadataConfig
	metadataLabel        string
}

type persistedFileResult struct {
	foundLabel bool
	fileExists bool
	filePath   string
}

// startContainerParams groups the inputs for starting (or recreating) the
// compose dev container.
type startContainerParams struct {
	parsedConfig        *config.SubstitutedConfig
	substitutionContext *config.SubstitutionContext
	project             *composetypes.Project
	composeHelper       *compose.ComposeHelper
	composeGlobalArgs   []string
	container           *config.ContainerDetails
	options             UpOptions
}

// buildAndExtendParams groups the inputs for building and feature-extending a
// compose service.
type buildAndExtendParams struct {
	parsedConfig        *config.SubstitutedConfig
	substitutionContext *config.SubstitutionContext
	project             *composetypes.Project
	composeHelper       *compose.ComposeHelper
	composeService      *composetypes.ServiceConfig
	globalArgs          []string
	featureSecretsFile  string
	// pull re-pulls base images during the compose build (--pull), set from
	// CLIOptions.Pull.
	pull bool
}

// composeUpParams groups the inputs shared by extendedDockerComposeUp and
// generateDockerComposeUpProject for producing the compose "up" override.
type composeUpParams struct {
	parsedConfig      *config.SubstitutedConfig
	mergedConfig      *config.MergedDevContainerConfig
	composeHelper     *compose.ComposeHelper
	composeService    *composetypes.ServiceConfig
	originalImageName string
	overrideImageName string
	imageDetails      *config.ImageDetails
	additionalLabels  map[string]string
}

func (r *runner) composeHelper() (*compose.ComposeHelper, error) {
	dockerDriver, ok := r.Driver.(driver.DockerDriver)
	if !ok {
		return nil, fmt.Errorf(
			"docker compose is not supported by this provider, choose a different one",
		)
	}

	return dockerDriver.ComposeHelper()
}

func (r *runner) stopDockerCompose(ctx context.Context, projectName string) error {
	composeHelper, err := r.composeHelper()
	if err != nil {
		return fmt.Errorf("find docker compose: %w", err)
	}

	parsedConfig, _, err := r.getSubstitutedConfig(r.WorkspaceConfig.CLIOptions)
	if err != nil {
		return fmt.Errorf("get parsed config: %w", err)
	}

	projFiles, err := r.dockerComposeProjectFiles(parsedConfig)
	if err != nil {
		return fmt.Errorf("get compose/env files: %w", err)
	}

	err = composeHelper.Stop(ctx, projectName, projFiles.composeGlobalArgs)
	if err != nil {
		return err
	}

	return nil
}

func (r *runner) deleteDockerCompose(
	ctx context.Context,
	projectName string,
	removeVolumes bool,
) error {
	composeHelper, err := r.composeHelper()
	if err != nil {
		return fmt.Errorf("find docker compose: %w", err)
	}

	parsedConfig, _, err := r.getSubstitutedConfig(r.WorkspaceConfig.CLIOptions)
	if err != nil {
		return fmt.Errorf("get parsed config: %w", err)
	}

	projFiles, err := r.dockerComposeProjectFiles(parsedConfig)
	if err != nil {
		return fmt.Errorf("get compose/env files: %w", err)
	}

	err = composeHelper.Remove(ctx, projectName, projFiles.composeGlobalArgs, removeVolumes)
	if err != nil {
		return err
	}

	return nil
}

func (r *runner) dockerComposeProjectFiles(
	parsedConfig *config.SubstitutedConfig,
) (composeProjectFiles, error) {
	envFiles := r.getEnvFiles()

	composeFiles, err := r.getDockerComposeFilePaths(parsedConfig, envFiles)
	if err != nil {
		return composeProjectFiles{}, fmt.Errorf("get docker compose file paths: %w", err)
	}

	var args []string
	for _, configFile := range composeFiles {
		args = append(args, "-f", configFile)
	}

	for _, envFile := range envFiles {
		args = append(args, "--env-file", envFile)
	}

	return composeProjectFiles{
		composeFiles:      composeFiles,
		envFiles:          envFiles,
		composeGlobalArgs: args,
	}, nil
}

func (r *runner) runDockerCompose(
	ctx context.Context,
	runParams *runContainerParams,
) (*config.Result, error) {
	parsedConfig := runParams.parsedConfig

	composeHelper, err := r.composeHelper()
	if err != nil {
		return nil, fmt.Errorf("find docker compose: %w", err)
	}

	projFiles, err := r.dockerComposeProjectFiles(parsedConfig)
	if err != nil {
		return nil, fmt.Errorf("get compose/env files: %w", err)
	}

	project, err := r.loadComposeProject(ctx, composeHelper, parsedConfig, projFiles)
	if err != nil {
		return nil, err
	}

	containerDetails, err := r.ensureComposeContainer(ctx, &composeContainerParams{
		runParams:         runParams,
		composeHelper:     composeHelper,
		project:           project,
		composeGlobalArgs: projFiles.composeGlobalArgs,
	})
	if err != nil {
		return nil, err
	}

	return r.finalizeComposeContainer(ctx, runParams, project, containerDetails)
}

// loadComposeProject loads the docker compose project from the resolved compose
// and env files and names it after the workspace.
func (r *runner) loadComposeProject(
	ctx context.Context,
	composeHelper *compose.ComposeHelper,
	parsedConfig *config.SubstitutedConfig,
	projFiles composeProjectFiles,
) (*composetypes.Project, error) {
	log.Debugf("Loading docker compose project %+v", projFiles.composeFiles)
	project, err := compose.LoadDockerComposeProject(
		ctx,
		projFiles.composeFiles,
		projFiles.envFiles,
	)
	if err != nil {
		return nil, fmt.Errorf("load docker compose project: %w", err)
	}
	project.Name = composeHelper.GetProjectName(r.ID)
	log.Debugf("Loaded project %s", project.Name)

	if err := validateRunServices(parsedConfig.Config.RunServices, project); err != nil {
		return nil, err
	}

	return project, nil
}

// composeContainerParams groups the inputs for ensuring a running compose dev
// container.
type composeContainerParams struct {
	runParams         *runContainerParams
	composeHelper     *compose.ComposeHelper
	project           *composetypes.Project
	composeGlobalArgs []string
}

// ensureComposeContainer finds the dev container and, when it is missing,
// stopped, or being recreated, starts it (reusing persisted project files when
// possible). It returns the resolved container details.
func (r *runner) ensureComposeContainer(
	ctx context.Context,
	params *composeContainerParams,
) (*config.ContainerDetails, error) {
	parsedConfig := params.runParams.parsedConfig
	options := params.runParams.options
	composeHelper := params.composeHelper
	project := params.project

	containerDetails, err := composeHelper.FindDevContainer(
		ctx,
		project.Name,
		parsedConfig.Config.Service,
	)
	if err != nil {
		return nil, fmt.Errorf("find dev container: %w", err)
	}

	// container already exists and is running, nothing to do
	if containerDetails != nil && containerDetails.State.Status == containerStatusRunning &&
		!options.Recreate {
		return containerDetails, nil
	}

	containerDetails, didStartProject := r.tryStartExistingProject(ctx, &existingProjectParams{
		parsedConfig:  parsedConfig,
		composeHelper: composeHelper,
		project:       project,
		container:     containerDetails,
		recreate:      options.Recreate,
	})
	if didStartProject {
		return containerDetails, nil
	}

	containerDetails, err = r.startContainer(ctx, &startContainerParams{
		parsedConfig:        parsedConfig,
		substitutionContext: params.runParams.substitutionContext,
		project:             project,
		composeHelper:       composeHelper,
		composeGlobalArgs:   params.composeGlobalArgs,
		container:           containerDetails,
		options:             options,
	})
	if err != nil {
		return nil, fmt.Errorf("start container: %w", err)
	}
	if containerDetails == nil {
		return nil, fmt.Errorf("couldn't find container after start")
	}

	return containerDetails, nil
}

// finalizeComposeContainer merges the container's metadata config and sets up
// the running container.
func (r *runner) finalizeComposeContainer(
	ctx context.Context,
	runParams *runContainerParams,
	project *composetypes.Project,
	containerDetails *config.ContainerDetails,
) (*config.Result, error) {
	parsedConfig := runParams.parsedConfig
	substitutionContext := runParams.substitutionContext
	options := runParams.options

	imageMetadataConfig, err := metadata.GetImageMetadataFromContainer(
		containerDetails,
		substitutionContext,
	)
	if err != nil {
		return nil, fmt.Errorf("get image metadata from container: %w", err)
	}

	if err := r.updateContainerUserUID(ctx, parsedConfig); err != nil {
		return nil, err
	}

	mergedConfig, err := mergeImageMetadataConfig(
		parsedConfig,
		imageMetadataConfig,
		options.ExtraDevContainerPath,
	)
	if err != nil {
		return nil, err
	}

	// expose the compose project name inside the container
	if mergedConfig.RemoteEnv == nil {
		mergedConfig.RemoteEnv = map[string]*string{}
	}
	composeName := project.Name
	mergedConfig.RemoteEnv["DEVSY_COMPOSE_PROJECT_NAME"] = &composeName
	composeAlias := project.Name
	mergedConfig.RemoteEnv["COMPOSE_PROJECT_NAME"] = &composeAlias

	hostWarnings, err := r.composeHostWarnings(parsedConfig, substitutionContext, options)
	if err != nil {
		return nil, err
	}

	return r.setupContainer(ctx, &setupContainerParams{
		rawConfig:           parsedConfig.Raw,
		containerDetails:    containerDetails,
		mergedConfig:        mergedConfig,
		substitutionContext: substitutionContext,
		timeout:             runParams.timeout,
		hostWarnings:        hostWarnings,
	})
}

// updateContainerUserUID updates the container user's UID/GID on Docker drivers.
func (r *runner) updateContainerUserUID(
	ctx context.Context,
	parsedConfig *config.SubstitutedConfig,
) error {
	dockerDriver, ok := r.Driver.(driver.DockerDriver)
	if !ok {
		return nil
	}
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()
	if err := dockerDriver.UpdateContainerUserUID(
		ctx,
		r.ID,
		parsedConfig.Config,
		writer,
	); err != nil {
		log.Errorf("failed to update container user UID/GID: error=%v", err)
		return err
	}
	return nil
}

// composeHostWarnings validates host requirements, returning warnings. When the
// requirements are unmet it errors unless SkipHostRequirements is set, in which
// case the error is downgraded to a warning.
func (r *runner) composeHostWarnings(
	parsedConfig *config.SubstitutedConfig,
	substitutionContext *config.SubstitutionContext,
	options UpOptions,
) ([]string, error) {
	hostWarnings, hostErr := config.ValidateHostRequirements(
		parsedConfig.Config.HostRequirements,
		config.SystemHostInfo{},
		substitutionContext.LocalWorkspaceFolder,
	)
	if hostErr != nil {
		if !options.SkipHostRequirements {
			return nil, hostErr
		}
		hostWarnings = append(hostWarnings, hostErr.Error())
	}
	return hostWarnings, nil
}

// existingProjectParams groups the inputs for attempting to start a compose
// project from previously persisted project files.
type existingProjectParams struct {
	parsedConfig  *config.SubstitutedConfig
	composeHelper *compose.ComposeHelper
	project       *composetypes.Project
	container     *config.ContainerDetails
	recreate      bool
}

// tryStartExistingProject attempts to bring up the dev container from project
// files discovered for an existing compose project, avoiding a full rebuild. It
// returns the (possibly updated) container details and whether the project was
// started. A false return means the caller should fall back to startContainer.
func (r *runner) tryStartExistingProject(
	ctx context.Context,
	params *existingProjectParams,
) (*config.ContainerDetails, bool) {
	composeHelper := params.composeHelper
	project := params.project
	containerDetails := params.container

	existingProjectFiles, err := composeHelper.FindProjectFiles(ctx, project.Name)
	if err != nil {
		log.Errorf("Error finding project files: %s", err)
		return containerDetails, false
	}
	if len(existingProjectFiles) == 0 || params.recreate {
		return containerDetails, false
	}

	log.Debugf("Found existing project files: %s", existingProjectFiles)
	if !allProjectFilesExist(existingProjectFiles) {
		// A referenced file is gone, so `compose up -f <missing>` would only
		// fail; rebuild from scratch instead.
		return containerDetails, false
	}

	// The project files are present, so `up` can reuse them. If it fails, the
	// caller falls back to rebuilding.
	details, err := r.composeUpExistingProject(ctx, params, existingProjectFiles)
	if err != nil || details == nil {
		// Compose failed, or reported success but the dev container is not
		// present; fall back to a full start rather than finalizing nil.
		return containerDetails, false
	}

	return details, true
}

// composeUpExistingProject runs "compose up" using the persisted project files
// and returns the resulting dev container details.
func (r *runner) composeUpExistingProject(
	ctx context.Context,
	params *existingProjectParams,
	existingProjectFiles []string,
) (*config.ContainerDetails, error) {
	upArgs := []string{composeProjectNameFlag, params.project.Name}
	for _, projectFile := range existingProjectFiles {
		upArgs = append(upArgs, "-f", projectFile)
	}
	upArgs = append(upArgs, "up", "-d")
	upArgs = r.onlyRunServices(upArgs, params.parsedConfig)

	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()
	if err := params.composeHelper.Run(ctx, upArgs, nil, writer, writer); err != nil {
		log.Errorf("Error starting project: %s", err)
		return nil, err
	}

	// wait for running and get container details
	details, err := params.composeHelper.FindDevContainer(
		ctx,
		params.project.Name,
		params.parsedConfig.Config.Service,
	)
	if err != nil {
		log.Errorf("Error finding dev container: %s", err)
		return nil, err
	}

	return details, nil
}

// allProjectFilesExist reports whether every persisted project file is still
// present on disk; a missing file means the project must be recreated.
func allProjectFilesExist(projectFiles []string) bool {
	for _, file := range projectFiles {
		if _, err := os.Stat(file); err != nil {
			log.Warnf("Project file %s does not exist anymore, recreating project", file)
			return false
		}
	}
	return true
}

func validateRunServices(runServices []string, project *composetypes.Project) error {
	if len(runServices) == 0 {
		return nil
	}
	var invalid []string
	for _, svc := range runServices {
		if _, ok := project.Services[svc]; !ok {
			invalid = append(invalid, svc)
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("runServices: service(s) not found in compose file: %v", invalid)
	}
	return nil
}

// onlyRunServices appends the services defined in .devcontainer.json runServices to the upArgs.
func (r *runner) onlyRunServices(upArgs []string, parsedConfig *config.SubstitutedConfig) []string {
	if len(parsedConfig.Config.RunServices) > 0 {
		// Run the main devcontainer
		upArgs = append(upArgs, parsedConfig.Config.Service)
		// Run the services defined in .devcontainer.json runServices
		for _, service := range parsedConfig.Config.RunServices {
			if service == parsedConfig.Config.Service {
				continue
			}
			upArgs = append(upArgs, service)
		}
	}
	return upArgs
}

func (r *runner) getDockerComposeFilePaths(
	parsedConfig *config.SubstitutedConfig,
	envFiles []string,
) ([]string, error) {
	// Prefer docker compose files declared in the devcontainer config.
	if len(parsedConfig.Config.DockerComposeFile) > 0 {
		return absoluteComposeFiles(
			filepath.Dir(parsedConfig.Config.Origin),
			parsedConfig.Config.DockerComposeFile,
		), nil
	}

	// Otherwise fall back to $COMPOSE_FILE from the environment or .env files.
	envComposeFile, err := composeFileFromEnv(envFiles)
	if err != nil {
		return nil, err
	}
	if envComposeFile != "" {
		return filepath.SplitList(envComposeFile), nil
	}

	return nil, nil
}

// absoluteComposeFiles resolves each compose file path against configFileDir
// unless it is already absolute.
func absoluteComposeFiles(configFileDir string, composeFiles []string) []string {
	resolved := make([]string, 0, len(composeFiles))
	for _, composeFile := range composeFiles {
		absPath := composeFile
		if !filepath.IsAbs(composeFile) {
			absPath = filepath.Join(configFileDir, composeFile)
		}
		resolved = append(resolved, absPath)
	}
	return resolved
}

// composeFileFromEnv returns the $COMPOSE_FILE value, preferring the process
// environment and falling back to the first .env file that defines it.
func composeFileFromEnv(envFiles []string) (string, error) {
	if envComposeFile := os.Getenv("COMPOSE_FILE"); envComposeFile != "" {
		return envComposeFile, nil
	}

	for _, envFile := range envFiles {
		env, err := godotenv.Read(envFile)
		if err != nil {
			return "", err
		}
		if env["COMPOSE_FILE"] != "" {
			return env["COMPOSE_FILE"], nil
		}
	}

	return "", nil
}

func (r *runner) getEnvFiles() []string {
	var envFiles []string
	envFile := path.Join(r.LocalWorkspaceFolder, ".env")
	envFileStat, err := os.Stat(envFile)
	if err == nil && envFileStat.Mode().IsRegular() {
		envFiles = append(envFiles, envFile)
	}
	return envFiles
}

// resolveComposeServiceImage looks up the named devcontainer service in the
// compose project and determines its original image name, falling back to the
// compose default image when the service does not declare one.
func resolveComposeServiceImage(
	project *composetypes.Project,
	composeHelper *compose.ComposeHelper,
	service string,
) (composetypes.ServiceConfig, string, error) {
	composeService, err := project.GetService(service)
	if err != nil {
		return composetypes.ServiceConfig{}, "", fmt.Errorf(
			"service %q configured in devcontainer.json not found in Docker Compose configuration",
			service,
		)
	}

	originalImageName := composeService.Image
	if originalImageName == "" {
		originalImageName, err = composeHelper.GetDefaultImage(project.Name, service)
		if err != nil {
			return composetypes.ServiceConfig{}, "", fmt.Errorf("get default image: %w", err)
		}
	}

	return composeService, originalImageName, nil
}

func (r *runner) startContainer(
	ctx context.Context,
	params *startContainerParams,
) (*config.ContainerDetails, error) {
	parsedConfig := params.parsedConfig
	project := params.project
	composeHelper := params.composeHelper
	composeGlobalArgs := params.composeGlobalArgs
	container := params.container
	options := params.options

	composeService, originalImageName, err := resolveComposeServiceImage(
		project,
		composeHelper,
		parsedConfig.Config.Service,
	)
	if err != nil {
		return nil, err
	}

	composeGlobalArgs, didRestoreFromPersistedShare := restorePersistedComposeArgs(
		container,
		composeGlobalArgs,
	)

	if container == nil || !didRestoreFromPersistedShare {
		composeGlobalArgs, err = r.buildComposeOverrideArgs(ctx, &composeOverrideParams{
			startParams:       params,
			composeService:    &composeService,
			originalImageName: originalImageName,
			composeGlobalArgs: composeGlobalArgs,
		})
		if err != nil {
			return nil, err
		}
	}

	if container != nil && options.Recreate {
		if err := r.recreateDevContainer(ctx, container); err != nil {
			return nil, err
		}
	}

	return r.composeUpAndFindContainer(ctx, &composeUpRunParams{
		project:              project,
		composeService:       &composeService,
		composeHelper:        composeHelper,
		composeGlobalArgs:    composeGlobalArgs,
		parsedConfig:         parsedConfig,
		hasExistingContainer: container != nil,
	})
}

// restorePersistedComposeArgs detects persisted feature override files recorded
// on an existing container and appends them as additional compose "-f" args.
// It reports whether a usable persisted share was found.
func restorePersistedComposeArgs(
	container *config.ContainerDetails,
	composeGlobalArgs []string,
) ([]string, bool) {
	if container == nil {
		return composeGlobalArgs, false
	}

	labels := container.Config.Labels
	if labels[ConfigFilesLabel] == "" {
		return composeGlobalArgs, false
	}

	configFiles := strings.Split(labels[ConfigFilesLabel], ",")
	persistedBuildFile := checkForPersistedFile(configFiles, FeaturesBuildOverrideFilePrefix)
	persistedStartFile := checkForPersistedFile(configFiles, FeaturesStartOverrideFilePrefix)

	usablePersistedShare := (persistedBuildFile.fileExists || !persistedBuildFile.foundLabel) &&
		persistedStartFile.fileExists
	if !usablePersistedShare {
		return composeGlobalArgs, false
	}

	if persistedBuildFile.fileExists {
		composeGlobalArgs = append(composeGlobalArgs, "-f", persistedBuildFile.filePath)
	}
	if persistedStartFile.fileExists {
		composeGlobalArgs = append(composeGlobalArgs, "-f", persistedStartFile.filePath)
	}
	return composeGlobalArgs, true
}

// composeOverrideParams groups the inputs for building the feature build/up
// override files and the resulting compose "-f" arguments.
type composeOverrideParams struct {
	startParams       *startContainerParams
	composeService    *composetypes.ServiceConfig
	originalImageName string
	composeGlobalArgs []string
}

// buildComposeOverrideArgs builds and feature-extends the compose project, then
// generates the "up" override, returning composeGlobalArgs extended with any
// generated override files.
func (r *runner) buildComposeOverrideArgs(
	ctx context.Context,
	params *composeOverrideParams,
) ([]string, error) {
	start := params.startParams
	composeGlobalArgs := params.composeGlobalArgs

	extendResult, err := r.buildAndExtendDockerCompose(ctx, &buildAndExtendParams{
		parsedConfig:        start.parsedConfig,
		substitutionContext: start.substitutionContext,
		project:             start.project,
		composeHelper:       start.composeHelper,
		composeService:      params.composeService,
		globalArgs:          composeGlobalArgs,
		featureSecretsFile:  start.options.FeatureSecretsFile,
		pull:                start.options.Pull,
	})
	if err != nil {
		return nil, fmt.Errorf("build and extend docker-compose: %w", err)
	}

	if extendResult.composeBuildFilePath != "" {
		composeGlobalArgs = append(composeGlobalArgs, "-f", extendResult.composeBuildFilePath)
	}

	currentImageName := extendResult.buildImageName
	if currentImageName == "" {
		currentImageName = params.originalImageName
	}

	imageDetails, err := r.inspectImage(ctx, currentImageName)
	if err != nil {
		return nil, fmt.Errorf("inspect image: %w", err)
	}

	overrideComposeUpFilePath, err := r.generateComposeUpOverride(
		params,
		extendResult,
		imageDetails,
	)
	if err != nil {
		return nil, err
	}

	if overrideComposeUpFilePath != "" {
		composeGlobalArgs = append(composeGlobalArgs, "-f", overrideComposeUpFilePath)
	}

	return composeGlobalArgs, nil
}

// generateComposeUpOverride merges the image metadata into the devcontainer
// config and writes the compose "up" override file, returning its path.
func (r *runner) generateComposeUpOverride(
	params *composeOverrideParams,
	extendResult composeExtendResult,
	imageDetails *config.ImageDetails,
) (string, error) {
	start := params.startParams

	mergedConfig, err := mergeImageMetadataConfig(
		start.parsedConfig,
		extendResult.imageMetadata,
		start.options.ExtraDevContainerPath,
	)
	if err != nil {
		return "", err
	}

	additionalLabels := map[string]string{
		metadata.ImageMetadataLabel: extendResult.metadataLabel,
		config.UserLabel:            imageDetails.Config.User,
	}
	overrideComposeUpFilePath, err := r.extendedDockerComposeUp(&composeUpParams{
		parsedConfig:      start.parsedConfig,
		mergedConfig:      mergedConfig,
		composeHelper:     start.composeHelper,
		composeService:    params.composeService,
		originalImageName: params.originalImageName,
		overrideImageName: extendResult.buildImageName,
		imageDetails:      imageDetails,
		additionalLabels:  additionalLabels,
	})
	if err != nil {
		return "", fmt.Errorf("extend docker-compose up: %w", err)
	}

	return overrideComposeUpFilePath, nil
}

// mergeImageMetadataConfig folds any extra devcontainer config into the image
// metadata, merges it with the parsed config, and applies extra remote env,
// returning the resulting merged devcontainer config.
func mergeImageMetadataConfig(
	parsedConfig *config.SubstitutedConfig,
	imageMetadata *config.ImageMetadataConfig,
	extraDevContainerPath string,
) (*config.MergedDevContainerConfig, error) {
	if extraDevContainerPath != "" {
		if imageMetadata == nil {
			imageMetadata = &config.ImageMetadataConfig{}
		}
		extraConfig, err := config.ParseDevContainerJSONFile(extraDevContainerPath)
		if err != nil {
			return nil, err
		}
		config.AddConfigToImageMetadata(extraConfig, imageMetadata)
	}

	mergedConfig, err := config.MergeConfiguration(parsedConfig.Config, imageMetadata.Config)
	if err != nil {
		return nil, fmt.Errorf("merge configuration: %w", err)
	}

	if err := config.MergeExtraRemoteEnv(mergedConfig, extraDevContainerPath); err != nil {
		return nil, err
	}

	return mergedConfig, nil
}

// recreateDevContainer stops and deletes an existing dev container so it can be
// recreated, in response to the --recreate option.
func (r *runner) recreateDevContainer(
	ctx context.Context,
	container *config.ContainerDetails,
) error {
	log.Debugf("Deleting dev container %s due to --recreate", container.ID)

	if err := r.Driver.StopDevContainer(ctx, r.ID); err != nil {
		return fmt.Errorf("stop dev container: %w", err)
	}

	if err := r.Driver.DeleteDevContainer(ctx, r.ID); err != nil {
		return fmt.Errorf("delete dev container: %w", err)
	}
	return nil
}

// composeUpRunParams groups the inputs for the final "docker compose up" step.
type composeUpRunParams struct {
	project              *composetypes.Project
	composeService       *composetypes.ServiceConfig
	composeHelper        *compose.ComposeHelper
	composeGlobalArgs    []string
	parsedConfig         *config.SubstitutedConfig
	hasExistingContainer bool
}

// composeUpAndFindContainer runs "docker compose up -d" with the assembled
// arguments and returns the resulting dev container details.
func (r *runner) composeUpAndFindContainer(
	ctx context.Context,
	params *composeUpRunParams,
) (*config.ContainerDetails, error) {
	upArgs := []string{composeProjectNameFlag, params.project.Name}
	upArgs = append(upArgs, params.composeGlobalArgs...)
	upArgs = append(upArgs, "up", "-d")
	if params.hasExistingContainer {
		upArgs = append(upArgs, "--no-recreate")
	}
	upArgs = r.onlyRunServices(upArgs, params.parsedConfig)

	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()
	if err := params.composeHelper.Run(ctx, upArgs, nil, writer, writer); err != nil {
		return nil, fmt.Errorf("docker-compose run: %w", err)
	}

	// TODO wait for started event?
	containerDetails, err := params.composeHelper.FindDevContainer(
		ctx,
		params.project.Name,
		params.composeService.Name,
	)
	if err != nil {
		return nil, fmt.Errorf("find dev container: %w", err)
	}

	return containerDetails, nil
}

func checkForPersistedFile(
	files []string,
	prefix string,
) persistedFileResult {
	for _, file := range files {
		if !strings.HasPrefix(filepath.Base(file), prefix) {
			continue
		}

		stat, err := os.Stat(file)
		if err == nil && stat.Mode().IsRegular() {
			return persistedFileResult{foundLabel: true, fileExists: true, filePath: file}
		} else if os.IsNotExist(err) {
			return persistedFileResult{foundLabel: true, fileExists: false, filePath: file}
		}
	}

	return persistedFileResult{}
}

func getDockerComposeFolder(workspaceOriginFolder string) string {
	return filepath.Join(workspaceOriginFolder, ".docker-compose")
}

// writeComposeOverrideFile writes a compose override file into the workspace's
// docker-compose folder using a collision-safe unique name that retains the
// given prefix (so checkForPersistedFile can still match it by prefix).
func (r *runner) writeComposeOverrideFile(prefix string, data []byte) (string, error) {
	dockerComposeFolder := getDockerComposeFolder(r.WorkspaceConfig.Origin)
	if err := os.MkdirAll(dockerComposeFolder, 0o750); err != nil {
		return "", err
	}

	f, err := os.CreateTemp(dockerComposeFolder, prefix+"-*.yml")
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Write(data); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}

	return f.Name(), nil
}

func mappingFromMap(m map[string]string) composetypes.MappingWithEquals {
	if len(m) == 0 {
		return nil
	}

	var values []string
	for k, v := range m {
		values = append(values, k+"="+v)
	}
	return composetypes.NewMappingWithEquals(values)
}

func mappingToMap(mapping composetypes.MappingWithEquals) map[string]string {
	ret := map[string]string{}
	for k, v := range mapping {
		ret[k] = *v
	}
	return ret
}

func isDockerComposeConfig(config *config.DevContainerConfig) bool {
	return len(config.DockerComposeFile) > 0
}
