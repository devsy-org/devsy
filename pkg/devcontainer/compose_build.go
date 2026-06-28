package devcontainer

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/devsy-org/devsy/pkg/compose"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/devsy-org/devsy/pkg/devcontainer/metadata"
	"github.com/devsy-org/devsy/pkg/dockerfile"
	"github.com/devsy-org/devsy/pkg/log"
	"gopkg.in/yaml.v3"
)

// featureFolderCopyPattern matches COPY/ADD directives that reference the
// devsy feature folder so they can be rewritten to a path relative to a custom
// build context. Compiled once because the folder name is a build-time constant.
var featureFolderCopyPattern = regexp.MustCompile(
	`(COPY|ADD)(\s+)\./` + regexp.QuoteMeta(config.DevsyContextFeatureFolder) + `/`,
)

// prepareComposeBuildInfo modifies a compose project's devcontainer Dockerfile
// to ensure it can be extended with features. If an Image is specified instead
// of a Build, the metadata from the Image is used to populate the build info.
func (r *runner) prepareComposeBuildInfo(
	ctx context.Context,
	subCtx *config.SubstitutionContext,
	composeService *composetypes.ServiceConfig,
	buildTarget string,
) (composeBuildInfo, error) {
	if composeService.Build == nil {
		imageBuildInfo, err := r.getImageBuildInfoFromImage(ctx, subCtx, composeService.Image)
		if err != nil {
			return composeBuildInfo{}, err
		}
		return composeBuildInfo{imageBuildInfo: imageBuildInfo, buildTarget: buildTarget}, nil
	}

	return r.prepareComposeDockerfileBuildInfo(subCtx, composeService)
}

// prepareComposeDockerfileBuildInfo handles the Build-backed branch of
// prepareComposeBuildInfo: it reads the service Dockerfile, resolves the build
// target (ensuring a final stage name for multi-stage builds), and extracts the
// image build info.
func (r *runner) prepareComposeDockerfileBuildInfo(
	subCtx *config.SubstitutionContext,
	composeService *composetypes.ServiceConfig,
) (composeBuildInfo, error) {
	dockerFilePath := composeService.Build.Dockerfile
	if !path.IsAbs(dockerFilePath) {
		dockerFilePath = filepath.Join(composeService.Build.Context, dockerFilePath)
	}

	// #nosec G304 -- dockerFilePath is derived from trusted devcontainer config.
	originalDockerfile, err := os.ReadFile(dockerFilePath)
	if err != nil {
		return composeBuildInfo{}, err
	}

	// Determine build target. If a multi stage build is used, ensure it is
	// valid and modify the Dockerfile if necessary.
	originalTarget := composeService.Build.Target
	var dockerfileContents string
	var buildTarget string
	if originalTarget != "" {
		buildTarget = originalTarget
		// Preserve the Dockerfile contents so a build-backed service with an
		// explicit target is not later misclassified as image-based.
		dockerfileContents = string(originalDockerfile)
	} else {
		lastStageName, modifiedDockerfile, stageErr := dockerfile.EnsureFinalStageName(
			string(originalDockerfile),
			config.DockerfileDefaultTarget,
		)
		if stageErr != nil {
			return composeBuildInfo{}, stageErr
		}

		buildTarget = lastStageName
		// Override Dockerfile if it was modified, otherwise use the original
		if modifiedDockerfile != "" {
			dockerfileContents = modifiedDockerfile
		} else {
			dockerfileContents = string(originalDockerfile)
		}
	}

	imageBuildInfo, err := r.getImageBuildInfoFromDockerfile(
		subCtx,
		string(originalDockerfile),
		mappingToMap(composeService.Build.Args),
		originalTarget,
	)
	if err != nil {
		return composeBuildInfo{}, err
	}

	return composeBuildInfo{
		imageBuildInfo:     imageBuildInfo,
		dockerfileContents: dockerfileContents,
		buildTarget:        buildTarget,
	}, nil
}

// This extends the build information for docker compose containers.
func (r *runner) buildAndExtendDockerCompose(
	ctx context.Context,
	params *buildAndExtendParams,
) (composeExtendResult, error) {
	prepared, err := r.prepareExtendedComposeBuild(ctx, params)
	if err != nil {
		return composeExtendResult{}, err
	}
	extendImageBuildInfo := prepared.extendImageBuildInfo

	buildImageName, err := composeBuildImageName(
		params.composeHelper,
		params.project.Name,
		params.composeService,
		hasFeatureBuildInfo(extendImageBuildInfo),
	)
	if err != nil {
		return composeExtendResult{}, err
	}

	dockerComposeFilePath, cleanup, err := r.composeFeatureOverride(
		prepared,
		params.composeService,
		buildImageName,
	)
	defer cleanup()
	if err != nil {
		return composeExtendResult{buildImageName: buildImageName}, err
	}

	buildArgs := composeBuildArgs(&composeBuildArgsParams{
		projectName:             params.project.Name,
		globalArgs:              params.globalArgs,
		overrideComposeFilePath: dockerComposeFilePath,
		pull:                    params.forceBuild,
		serviceName:             params.composeService.Name,
		runServices:             params.parsedConfig.Config.RunServices,
	})

	if err := r.runComposeBuild(ctx, params.composeHelper, buildArgs); err != nil {
		return composeExtendResult{buildImageName: buildImageName}, err
	}

	imageMetadata, err := metadata.GetDevContainerMetadata(
		params.substitutionContext,
		prepared.imageBuildInfo.Metadata,
		params.parsedConfig,
		extendImageBuildInfo.Features,
	)
	if err != nil {
		return composeExtendResult{buildImageName: buildImageName}, err
	}

	return composeExtendResult{
		buildImageName:       buildImageName,
		composeBuildFilePath: dockerComposeFilePath,
		imageMetadata:        imageMetadata,
		metadataLabel:        extendImageBuildInfo.MetadataLabel,
	}, nil
}

// composeFeatureOverride builds the feature override compose file when the build
// has features, returning the override path (empty when none) and a cleanup
// function that is always safe to defer.
func (r *runner) composeFeatureOverride(
	prepared preparedComposeBuild,
	composeService *composetypes.ServiceConfig,
	buildImageName string,
) (string, func(), error) {
	if !hasFeatureBuildInfo(prepared.extendImageBuildInfo) {
		return "", func() {}, nil
	}
	return r.writeFeatureBuildOverride(&featureBuildOverrideParams{
		extendImageBuildInfo: prepared.extendImageBuildInfo,
		composeService:       composeService,
		buildImageName:       buildImageName,
		dockerfileContents:   prepared.dockerfileContents,
		buildTarget:          prepared.buildTarget,
	})
}

// preparedComposeBuild holds the resolved base build info and feature-extended
// build info used to assemble the compose build.
type preparedComposeBuild struct {
	imageBuildInfo       *config.ImageBuildInfo
	extendImageBuildInfo *feature.ExtendedBuildInfo
	dockerfileContents   string
	buildTarget          string
}

// prepareExtendedComposeBuild resolves the base image build info for the compose
// service and computes the feature-extended build info on top of it.
func (r *runner) prepareExtendedComposeBuild(
	ctx context.Context,
	params *buildAndExtendParams,
) (preparedComposeBuild, error) {
	const defaultBuildTarget = "dev_container_auto_added_stage_label"

	buildInfo, err := r.prepareComposeBuildInfo(
		ctx,
		params.substitutionContext,
		params.composeService,
		defaultBuildTarget,
	)
	if err != nil {
		return preparedComposeBuild{}, err
	}

	extendImageBuildInfo, err := feature.GetExtendedBuildInfo(&feature.ExtendedBuildParams{
		Ctx:                params.substitutionContext,
		ImageBuildInfo:     buildInfo.imageBuildInfo,
		Target:             buildInfo.buildTarget,
		DevContainerConfig: params.parsedConfig,
		ForceBuild:         false,
		SecretOpts: &feature.SecretOptions{
			SecretsFile: params.featureSecretsFile,
			Prompter:    &feature.TerminalSecretPrompter{},
		},
	})
	if err != nil {
		return preparedComposeBuild{}, err
	}

	return preparedComposeBuild{
		imageBuildInfo:       buildInfo.imageBuildInfo,
		extendImageBuildInfo: extendImageBuildInfo,
		dockerfileContents:   buildInfo.dockerfileContents,
		buildTarget:          buildInfo.buildTarget,
	}, nil
}

// hasFeatureBuildInfo reports whether the extended build info carries a feature
// build that requires generating an override Dockerfile and compose file.
func hasFeatureBuildInfo(extendImageBuildInfo *feature.ExtendedBuildInfo) bool {
	return extendImageBuildInfo != nil && extendImageBuildInfo.FeaturesBuildInfo != nil
}

// runComposeBuild runs "docker compose ... build" with the given arguments,
// streaming output to the info log.
func (r *runner) runComposeBuild(
	ctx context.Context,
	composeHelper *compose.ComposeHelper,
	buildArgs []string,
) error {
	writer := log.Writer(log.LevelInfo)
	defer func() { _ = writer.Close() }()
	log.Debugf("Run %s %s", composeHelper.Command, strings.Join(buildArgs, " "))
	if err := composeHelper.Run(ctx, buildArgs, nil, writer, writer); err != nil {
		return err
	}
	return nil
}

// featureBuildOverrideParams groups the inputs for writing the extended
// Dockerfile and compose build override used when features are present.
type featureBuildOverrideParams struct {
	extendImageBuildInfo *feature.ExtendedBuildInfo
	composeService       *composetypes.ServiceConfig
	buildImageName       string
	dockerfileContents   string
	buildTarget          string
}

// writeFeatureBuildOverride writes the extended Dockerfile and the compose build
// override file referencing it. It returns the override compose file path and a
// cleanup function that removes the temporary extended Dockerfile directory. The
// cleanup function is always non-nil and safe to defer even on error.
func (r *runner) writeFeatureBuildOverride(
	params *featureBuildOverrideParams,
) (composeFilePath string, cleanup func(), err error) {
	cleanup = func() {}

	dockerfileContents := params.dockerfileContents
	// If the dockerfile is empty (because an Image was used), reference that
	// image as the build target after the features / modified contents.
	if dockerfileContents == "" {
		if params.composeService.Image == "" && params.composeService.Build == nil {
			return "", cleanup, fmt.Errorf(
				"compose service %q has no image or build configuration",
				params.composeService.Name,
			)
		}
		sanitizedImage := strings.ReplaceAll(
			strings.ReplaceAll(params.composeService.Image, "\n", ""),
			"\r",
			"",
		)
		dockerfileContents = fmt.Sprintf("FROM %s AS %s\n", sanitizedImage, params.buildTarget)
	}

	// The feature build info rewrites the Dockerfile path, so the original path
	// is intentionally empty here.
	extendedDockerfilePath, extendedDockerfileContent := r.extendedDockerfile(
		params.extendImageBuildInfo.FeaturesBuildInfo,
		"",
		dockerfileContents,
	)

	log.Debugf(
		"Creating extended Dockerfile %s with content: \n %s",
		extendedDockerfilePath,
		extendedDockerfileContent,
	)

	cleanup = func() { _ = os.RemoveAll(filepath.Dir(extendedDockerfilePath)) }

	composeFilePath, err = r.extendedDockerComposeBuild(&extendedComposeBuildParams{
		composeService:    params.composeService,
		buildImageName:    params.buildImageName,
		dockerFilePath:    extendedDockerfilePath,
		dockerfileContent: extendedDockerfileContent,
		featuresBuildInfo: params.extendImageBuildInfo.FeaturesBuildInfo,
	})
	if err != nil {
		return "", cleanup, err
	}

	return composeFilePath, cleanup, nil
}

// composeBuildArgsParams groups the inputs for assembling compose build args.
type composeBuildArgsParams struct {
	projectName             string
	globalArgs              []string
	overrideComposeFilePath string
	pull                    bool
	serviceName             string
	runServices             []string
}

// composeBuildArgs assembles the "docker compose ... build" argument list,
// adding the override file, --pull when a fresh base image is requested, and
// any explicitly requested run services.
func composeBuildArgs(params *composeBuildArgsParams) []string {
	buildArgs := []string{composeProjectNameFlag, params.projectName}
	buildArgs = append(buildArgs, params.globalArgs...)
	if params.overrideComposeFilePath != "" {
		buildArgs = append(buildArgs, "-f", params.overrideComposeFilePath)
	}
	buildArgs = append(buildArgs, "build")
	if params.pull {
		buildArgs = append(buildArgs, "--pull")
	}

	// Only run the services defined in .devcontainer.json runServices
	if len(params.runServices) > 0 {
		buildArgs = append(buildArgs, params.serviceName)
		for _, service := range params.runServices {
			if service == params.serviceName {
				continue
			}
			buildArgs = append(buildArgs, service)
		}
	}
	return buildArgs
}

func (r *runner) extendedDockerfile(
	featureBuildInfo *feature.BuildInfo,
	dockerfilePath, dockerfileContent string,
) (string, string) {
	// extra args?
	finalDockerfilePath := dockerfilePath
	finalDockerfileContent := dockerfileContent

	// get extended build info
	if featureBuildInfo != nil {
		// rewrite dockerfile path
		finalDockerfilePath = filepath.Join(
			featureBuildInfo.FeaturesFolder,
			"Dockerfile-with-features",
		)

		// rewrite dockerfile
		finalDockerfileContent = dockerfile.RemoveSyntaxVersion(dockerfileContent)
		finalDockerfileContent = strings.TrimSpace(strings.Join([]string{
			featureBuildInfo.DockerfilePrefixContent,
			strings.TrimSpace(finalDockerfileContent),
			featureBuildInfo.DockerfileContent,
		}, "\n"))
	}

	return finalDockerfilePath, finalDockerfileContent
}

func (r *runner) setBuildPathsForContext(
	originalContext, dockerFilePath, dockerfileContent, featuresFolder string,
) (relDockerfilePath string, modifiedDockerfileContent string, err error) {
	absBuildContext, err := filepath.Abs(originalContext)
	if err != nil {
		return "", "", err
	}

	absDockerFilePath, err := filepath.Abs(dockerFilePath)
	if err != nil {
		return "", "", err
	}
	relDockerfilePath, err = filepath.Rel(absBuildContext, absDockerFilePath)
	if err != nil {
		return "", "", err
	}

	absFeatureFolder, err := filepath.Abs(featuresFolder)
	if err != nil {
		return "", "", err
	}
	relFeaturePath, err := filepath.Rel(absBuildContext, absFeatureFolder)
	if err != nil {
		return "", "", err
	}

	// Rewrite COPY/ADD directives that reference the features folder to use the relative path
	// from the custom build context. This ensures that the features folder is referenced in the
	// Dockerfile.
	modifiedDockerfileContent = featureFolderCopyPattern.ReplaceAllString(
		dockerfileContent,
		"${1}${2}./"+filepath.ToSlash(relFeaturePath)+"/",
	)

	return relDockerfilePath, modifiedDockerfileContent, nil
}

type buildContextResult struct {
	context                 string
	dockerfilePathInContext string
	dockerfileContent       string
}

// extendedComposeBuildParams groups the inputs for writing the extended compose
// build file referencing a feature-augmented Dockerfile.
type extendedComposeBuildParams struct {
	composeService    *composetypes.ServiceConfig
	buildImageName    string
	dockerFilePath    string
	dockerfileContent string
	featuresBuildInfo *feature.BuildInfo
}

func (r *runner) extendedDockerComposeBuild(params *extendedComposeBuildParams) (string, error) {
	result, err := r.prepareBuildContext(
		params.composeService,
		params.dockerFilePath,
		params.dockerfileContent,
		params.featuresBuildInfo,
	)
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(
		params.dockerFilePath,
		[]byte(result.dockerfileContent),
		0o600,
	); err != nil {
		return "", err
	}

	service := r.createComposeService(&composeServiceParams{
		composeService:          params.composeService,
		buildImageName:          params.buildImageName,
		dockerfilePathInContext: result.dockerfilePathInContext,
		buildContext:            result.context,
		featuresBuildInfo:       params.featuresBuildInfo,
	})
	return r.writeComposeFile(service)
}

func (r *runner) prepareBuildContext(
	composeService *composetypes.ServiceConfig,
	dockerFilePath, dockerfileContent string,
	featuresBuildInfo *feature.BuildInfo,
) (*buildContextResult, error) {
	buildContext := filepath.Dir(featuresBuildInfo.FeaturesFolder)
	relDockerFilePath, err := filepath.Rel(buildContext, dockerFilePath)
	if err != nil {
		return nil, err
	}

	result := &buildContextResult{
		context:                 buildContext,
		dockerfilePathInContext: relDockerFilePath,
		dockerfileContent:       dockerfileContent,
	}

	if composeService.Build != nil && composeService.Build.Context != "" {
		relDockerFilePath, modifiedDockerfileContent, err := r.setBuildPathsForContext(
			composeService.Build.Context,
			dockerFilePath,
			dockerfileContent,
			featuresBuildInfo.FeaturesFolder,
		)
		if err != nil {
			return nil, err
		}
		log.Debugf(
			"modified Dockerfile path in context to %s and content for extended compose build context %s",
			relDockerFilePath,
			composeService.Build.Context,
		)
		result.context = composeService.Build.Context
		result.dockerfilePathInContext = relDockerFilePath
		result.dockerfileContent = modifiedDockerfileContent
	}

	return result, nil
}

// composeServiceParams groups the inputs for building the override compose
// service definition.
type composeServiceParams struct {
	composeService          *composetypes.ServiceConfig
	buildImageName          string
	dockerfilePathInContext string
	buildContext            string
	featuresBuildInfo       *feature.BuildInfo
}

func (r *runner) createComposeService(params *composeServiceParams) *composetypes.ServiceConfig {
	composeService := params.composeService
	featuresBuildInfo := params.featuresBuildInfo

	service := &composetypes.ServiceConfig{
		Name: composeService.Name,
		Build: &composetypes.BuildConfig{
			Dockerfile: params.dockerfilePathInContext,
			Context:    params.buildContext,
		},
	}
	if params.buildImageName != "" {
		service.Image = stripDigestFromImageRef(params.buildImageName)
	}

	if composeService.Build != nil && composeService.Build.Target != "" {
		service.Build.Target = featuresBuildInfo.OverrideTarget
	}

	service.Build.Args = composetypes.NewMappingWithEquals([]string{"BUILDKIT_INLINE_CACHE=1"})
	for k, v := range featuresBuildInfo.BuildArgs {
		service.Build.Args[k] = &v
	}

	return service
}

func composeBuildImageName(
	composeHelper *compose.ComposeHelper,
	projectName string,
	composeService *composetypes.ServiceConfig,
	hasFeatures bool,
) (string, error) {
	if hasFeatures && composeService.Image != "" && composeService.Build == nil {
		return composeHelper.GetDefaultImage(projectName, composeService.Name)
	}

	if composeService.Image != "" {
		return composeService.Image, nil
	}

	return composeHelper.GetDefaultImage(projectName, composeService.Name)
}

func (r *runner) writeComposeFile(service *composetypes.ServiceConfig) (string, error) {
	project := &composetypes.Project{
		Services: map[string]composetypes.ServiceConfig{
			service.Name: *service,
		},
	}

	dockerComposeData, err := yaml.Marshal(project)
	if err != nil {
		return "", err
	}

	dockerComposePath, err := r.writeComposeOverrideFile(
		FeaturesBuildOverrideFilePrefix,
		dockerComposeData,
	)
	if err != nil {
		return "", err
	}

	log.Debugf(
		"Creating docker-compose build %s with content:\n %s",
		dockerComposePath,
		string(dockerComposeData),
	)

	return dockerComposePath, nil
}

func stripDigestFromImageRef(imageRef string) string {
	baseRef, _, found := strings.Cut(imageRef, "@")
	if !found {
		return imageRef
	}

	return baseRef
}
