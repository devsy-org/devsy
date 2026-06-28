package devcontainer

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/build"
	"github.com/devsy-org/devsy/pkg/devcontainer/buildkit"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/feature"
	"github.com/devsy-org/devsy/pkg/devcontainer/metadata"
	"github.com/devsy-org/devsy/pkg/dockerfile"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/image"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
)

func (r *runner) build(
	ctx context.Context,
	parsedConfig *config.SubstitutedConfig,
	substitutionContext *config.SubstitutionContext,
	options provider.BuildOptions,
) (*config.BuildInfo, error) {
	var buildInfo *config.BuildInfo
	var err error

	switch {
	case isDockerFileConfig(parsedConfig.Config):
		buildInfo, err = r.buildAndExtendImage(ctx, parsedConfig, substitutionContext, options)
	case isDockerComposeConfig(parsedConfig.Config):
		buildInfo, err = r.buildDevImageCompose(ctx, parsedConfig, substitutionContext, options)
	default:
		buildInfo, err = r.extendImage(ctx, parsedConfig, substitutionContext, options)
	}

	if err != nil {
		return nil, err
	}

	// Add extra devcontainer config if provided
	if options.ExtraDevContainerPath != "" {
		if buildInfo.ImageMetadata == nil {
			buildInfo.ImageMetadata = &config.ImageMetadataConfig{}
		}
		extraConfig, err := config.ParseDevContainerJSONFile(options.ExtraDevContainerPath)
		if err != nil {
			return nil, err
		}
		config.AddConfigToImageMetadata(extraConfig, buildInfo.ImageMetadata)
	}

	return buildInfo, nil
}

func (r *runner) extendImage(
	ctx context.Context,
	parsedConfig *config.SubstitutedConfig,
	substitutionContext *config.SubstitutionContext,
	options provider.BuildOptions,
) (*config.BuildInfo, error) {
	imageBase := parsedConfig.Config.Image
	imageBuildInfo, err := r.getImageBuildInfoFromImage(ctx, substitutionContext, imageBase)
	if err != nil {
		return nil, fmt.Errorf("get image build info: %w", err)
	}

	// get extend image build info
	extendedBuildInfo, err := feature.GetExtendedBuildInfo(&feature.ExtendedBuildParams{
		Ctx:                substitutionContext,
		ImageBuildInfo:     imageBuildInfo,
		Target:             imageBase,
		DevContainerConfig: parsedConfig,
		ForceBuild:         options.ForceBuild,
		SecretOpts:         featureSecretOpts(options),
	})
	if err != nil {
		return nil, fmt.Errorf("get extended build info: %w", err)
	}

	// no need to build here
	if extendedBuildInfo == nil || extendedBuildInfo.FeaturesBuildInfo == nil {
		return &config.BuildInfo{
			ImageDetails:  imageBuildInfo.ImageDetails,
			ImageMetadata: extendedBuildInfo.MetadataConfig,
			ImageName:     imageBase,
			RegistryCache: options.RegistryCache,
			Tags:          options.Tag,
		}, nil
	}

	// build the image
	return r.buildImage(ctx, &buildImageParams{
		parsedConfig:        parsedConfig,
		substitutionContext: substitutionContext,
		buildInfo:           imageBuildInfo,
		extendedBuildInfo:   extendedBuildInfo,
		options:             options,
	})
}

// dockerfileBuildBase holds the resolved Dockerfile and the image base/target
// stage for a dockerfile-backed build.
type dockerfileBuildBase struct {
	path      string
	content   []byte
	imageBase string
}

// resolveDockerfileBuildBase locates and reads the Dockerfile and determines the
// image base/target stage, ensuring a final stage name when no target is set.
func (r *runner) resolveDockerfileBuildBase(
	parsedConfig *config.SubstitutedConfig,
) (*dockerfileBuildBase, error) {
	dockerFilePath, err := r.getDockerfilePath(parsedConfig.Config)
	if err != nil {
		return nil, err
	}

	// #nosec G304 -- dockerFilePath is derived from trusted devcontainer config.
	dockerFileContent, err := os.ReadFile(dockerFilePath)
	if err != nil {
		return nil, err
	}

	if target := parsedConfig.Config.GetTarget(); target != "" {
		return &dockerfileBuildBase{
			path:      dockerFilePath,
			content:   dockerFileContent,
			imageBase: target,
		}, nil
	}

	lastTargetName, modifiedDockerfileContents, err := dockerfile.EnsureFinalStageName(
		string(dockerFileContent),
		config.DockerfileDefaultTarget,
	)
	if err != nil {
		return nil, err
	}
	if modifiedDockerfileContents != "" {
		dockerFileContent = []byte(modifiedDockerfileContents)
	}

	return &dockerfileBuildBase{
		path:      dockerFilePath,
		content:   dockerFileContent,
		imageBase: lastTargetName,
	}, nil
}

func (r *runner) buildAndExtendImage(
	ctx context.Context,
	parsedConfig *config.SubstitutedConfig,
	substitutionContext *config.SubstitutionContext,
	options provider.BuildOptions,
) (*config.BuildInfo, error) {
	base, err := r.resolveDockerfileBuildBase(parsedConfig)
	if err != nil {
		return nil, err
	}

	// get image build info
	imageBuildInfo, err := r.getImageBuildInfoFromDockerfile(
		substitutionContext,
		string(base.content),
		parsedConfig.Config.GetArgs(),
		parsedConfig.Config.GetTarget(),
	)
	if err != nil {
		return nil, fmt.Errorf("get image build info: %w", err)
	}

	// get extend image build info
	extendedBuildInfo, err := feature.GetExtendedBuildInfo(&feature.ExtendedBuildParams{
		Ctx:                substitutionContext,
		ImageBuildInfo:     imageBuildInfo,
		Target:             base.imageBase,
		DevContainerConfig: parsedConfig,
		ForceBuild:         options.ForceBuild,
		SecretOpts:         featureSecretOpts(options),
	})
	if err != nil {
		return nil, fmt.Errorf("get extended build info: %w", err)
	}

	// build the image
	return r.buildImage(ctx, &buildImageParams{
		parsedConfig:        parsedConfig,
		substitutionContext: substitutionContext,
		buildInfo:           imageBuildInfo,
		extendedBuildInfo:   extendedBuildInfo,
		dockerfilePath:      base.path,
		dockerfileContent:   string(base.content),
		options:             options,
	})
}

func (r *runner) getDockerfilePath(parsedConfig *config.DevContainerConfig) (string, error) {
	if parsedConfig.Origin == "" {
		return "", fmt.Errorf(
			"config origin path is empty, cannot resolve dockerfile location without knowing " +
				"where devcontainer config was loaded from",
		)
	}

	dockerfilePathFromConfig := parsedConfig.GetDockerfile()
	var dockerfilePath string
	if filepath.IsAbs(dockerfilePathFromConfig) {
		log.Debugf(
			"using absolute dockerfile path: dockerfilePath=%s, pathType=absolute",
			dockerfilePathFromConfig,
		)
		dockerfilePath = dockerfilePathFromConfig
	} else {
		configFileDir := filepath.Dir(parsedConfig.Origin)
		dockerfilePath = filepath.Join(configFileDir, dockerfilePathFromConfig)
		log.Debugf(
			"using relative dockerfile path: dockerfilePath=%s, configDir=%s, pathType=relative",
			dockerfilePathFromConfig,
			configFileDir,
		)
	}

	log.Debugf("resolved dockerfile path: finalPath=%s", dockerfilePath)

	_, err := os.Stat(dockerfilePath)
	if err != nil {
		return "", fmt.Errorf("dockerfile not found at %s: %w", dockerfilePath, err)
	}

	return dockerfilePath, nil
}

func (r *runner) getImageBuildInfoFromImage(
	ctx context.Context,
	substitutionContext *config.SubstitutionContext,
	imageName string,
) (*config.ImageBuildInfo, error) {
	imageDetails, err := r.inspectImage(ctx, imageName)
	if err != nil {
		return nil, err
	}

	user := "root"
	if imageDetails.Config.User != "" {
		user = imageDetails.Config.User
	}

	imageMetadata, err := metadata.GetImageMetadata(imageDetails, substitutionContext)
	if err != nil {
		return nil, fmt.Errorf("get image metadata: %w", err)
	}

	return &config.ImageBuildInfo{
		ImageDetails: imageDetails,
		User:         user,
		Metadata:     imageMetadata,
	}, nil
}

func (r *runner) getImageBuildInfoFromDockerfile(
	substitutionContext *config.SubstitutionContext,
	dockerFileContent string,
	buildArgs map[string]string,
	target string,
) (*config.ImageBuildInfo, error) {
	parsedDockerfile, err := dockerfile.Parse(dockerFileContent)
	if err != nil {
		return nil, fmt.Errorf("parse dockerfile: %w", err)
	}

	if err := validateDockerfileTarget(parsedDockerfile, target); err != nil {
		return nil, err
	}

	baseImage := parsedDockerfile.FindBaseImage(buildArgs, target)
	if baseImage == "" {
		return nil, fmt.Errorf("find base image %s", target)
	}

	imageDetails, err := r.inspectImage(context.TODO(), baseImage)
	if err != nil {
		return nil, fmt.Errorf("inspect image %s: %w", baseImage, err)
	}

	user := resolveDockerfileUser(parsedDockerfile, buildArgs, imageDetails, target)

	// parse metadata from image details
	imageMetadataConfig, err := metadata.GetImageMetadata(imageDetails, substitutionContext)
	if err != nil {
		return nil, fmt.Errorf("get image metadata: %w", err)
	}

	return &config.ImageBuildInfo{
		Dockerfile: parsedDockerfile,
		User:       user,
		Metadata:   imageMetadataConfig,
	}, nil
}

// validateDockerfileTarget ensures a non-empty build target exists in the
// parsed Dockerfile's stages.
func validateDockerfileTarget(parsed *dockerfile.Dockerfile, target string) error {
	if target == "" || parsed.StagesByTarget == nil {
		return nil
	}
	if _, ok := parsed.StagesByTarget[target]; !ok {
		return fmt.Errorf("build target does not exist")
	}
	return nil
}

// resolveDockerfileUser determines the build user, preferring a USER statement
// in the Dockerfile, then the base image's user, then "root".
func resolveDockerfileUser(
	parsed *dockerfile.Dockerfile,
	buildArgs map[string]string,
	imageDetails *config.ImageDetails,
	target string,
) string {
	user := parsed.FindUserStatement(
		buildArgs,
		config.ListToObject(imageDetails.Config.Env),
		target,
	)
	if user == "" {
		user = imageDetails.Config.User
	}
	if user == "" {
		user = "root"
	}
	return user
}

// prebuildLookupParams groups the inputs for locating an existing prebuild image.
type prebuildLookupParams struct {
	parsedConfig      *config.SubstitutedConfig
	extendedBuildInfo *feature.ExtendedBuildInfo
	options           provider.BuildOptions
	prebuildHash      string
	targetArch        string
}

// findPrebuildImage searches the configured prebuild repositories for an image
// matching the prebuild hash and target architecture. It returns the resolved
// BuildInfo when found, or (nil, nil) when no prebuild image is available.
func (r *runner) findPrebuildImage(
	ctx context.Context,
	params *prebuildLookupParams,
) (*config.BuildInfo, error) {
	options := params.options
	devsyCustomizations := config.GetDevsyCustomizations(params.parsedConfig.Config)
	if options.Repository != "" {
		options.PrebuildRepositories = append(options.PrebuildRepositories, options.Repository)
	}
	options.PrebuildRepositories = append(
		options.PrebuildRepositories,
		devsyCustomizations.PrebuildRepository...)

	log.Debugf(
		"Try to find prebuild image %s in repositories %s",
		params.prebuildHash,
		strings.Join(options.PrebuildRepositories, ","),
	)
	for _, prebuildRepo := range options.PrebuildRepositories {
		prebuildImage := prebuildRepo + ":" + params.prebuildHash
		img, err := image.GetImageForArch(ctx, prebuildImage, params.targetArch)
		if err != nil {
			log.Debugf("Error trying to find prebuild image %s: %v", prebuildImage, err)
			continue
		}
		if img == nil {
			continue
		}

		log.Infof("Found existing prebuilt image %s", prebuildImage)
		imageDetails, err := r.inspectImage(ctx, prebuildImage)
		if err != nil {
			return nil, fmt.Errorf("get image details: %w", err)
		}

		return &config.BuildInfo{
			ImageDetails:  imageDetails,
			ImageMetadata: params.extendedBuildInfo.MetadataConfig,
			ImageName:     prebuildImage,
			PrebuildHash:  params.prebuildHash,
			RegistryCache: options.RegistryCache,
			Tags:          options.Tag,
		}, nil
	}

	return nil, nil
}

// buildImageParams groups the inputs for building a dockerfile-backed image.
type buildImageParams struct {
	parsedConfig        *config.SubstitutedConfig
	substitutionContext *config.SubstitutionContext
	buildInfo           *config.ImageBuildInfo
	extendedBuildInfo   *feature.ExtendedBuildInfo
	dockerfilePath      string
	dockerfileContent   string
	options             provider.BuildOptions
}

func (r *runner) buildImage(
	ctx context.Context,
	params *buildImageParams,
) (*config.BuildInfo, error) {
	parsedConfig := params.parsedConfig
	options := params.options

	targetArch, err := r.Driver.TargetArchitecture(ctx, r.ID)
	if err != nil {
		return nil, err
	}

	prebuildHash, err := config.CalculatePrebuildHash(config.PrebuildHashParams{
		Config:            parsedConfig.Config,
		Platform:          options.Platform,
		Architecture:      targetArch,
		ContextPath:       config.GetContextPath(parsedConfig.Config),
		DockerfilePath:    params.dockerfilePath,
		DockerfileContent: params.dockerfileContent,
		BuildInfo:         params.buildInfo,
	})
	if err != nil {
		return nil, err
	}

	// check if there is a prebuild image
	if !options.ForceDockerless && !options.ForceBuild {
		prebuilt, err := r.findPrebuildImage(ctx, &prebuildLookupParams{
			parsedConfig:      parsedConfig,
			extendedBuildInfo: params.extendedBuildInfo,
			options:           options,
			prebuildHash:      prebuildHash,
			targetArch:        targetArch,
		})
		if err != nil {
			return nil, err
		}
		if prebuilt != nil {
			return prebuilt, nil
		}
	}

	return r.executeBuild(ctx, params, prebuildHash, targetArch)
}

// executeBuild dispatches the actual image build to the appropriate backend:
// remote BuildKit (platform mode), the dockerless fallback (non-docker driver),
// or the docker driver.
func (r *runner) executeBuild(
	ctx context.Context,
	params *buildImageParams,
	prebuildHash, targetArch string,
) (*config.BuildInfo, error) {
	options := params.options

	if options.CLIOptions.Platform.Enabled {
		buildInfo, err := buildkit.BuildRemote(ctx, buildkit.BuildRemoteOptions{
			PrebuildHash:         prebuildHash,
			ParsedConfig:         params.parsedConfig,
			ExtendedBuildInfo:    params.extendedBuildInfo,
			DockerfilePath:       params.dockerfilePath,
			DockerfileContent:    params.dockerfileContent,
			LocalWorkspaceFolder: r.LocalWorkspaceFolder,
			Options:              options,
			TargetArch:           targetArch,
		})
		if err != nil {
			return nil, fmt.Errorf("(remote): %w", err)
		}

		return buildInfo, nil
	}

	// check if we should fallback to dockerless.
	// This should only be OSS kubernetes as of March 06, 2025.
	dockerDriver, ok := r.Driver.(driver.DockerDriver)
	if options.ForceDockerless || !ok {
		if r.WorkspaceConfig.Agent.Dockerless.Disabled == pkgconfig.BoolTrue {
			return nil, fmt.Errorf(
				"cannot build devcontainer because driver is non-docker and dockerless fallback is disabled",
			)
		}

		return dockerlessFallback(&dockerlessFallbackParams{
			localWorkspaceFolder:     r.LocalWorkspaceFolder,
			containerWorkspaceFolder: params.substitutionContext.ContainerWorkspaceFolder,
			parsedConfig:             params.parsedConfig,
			buildInfo:                params.buildInfo,
			extendedBuildInfo:        params.extendedBuildInfo,
			dockerfileContent:        params.dockerfileContent,
			options:                  options,
		})
	}

	return dockerDriver.BuildDevContainer(ctx, driver.BuildRequest{
		PrebuildHash:         prebuildHash,
		ParsedConfig:         params.parsedConfig,
		ExtendedBuildInfo:    params.extendedBuildInfo,
		DockerfilePath:       params.dockerfilePath,
		DockerfileContent:    params.dockerfileContent,
		LocalWorkspaceFolder: r.LocalWorkspaceFolder,
		Options:              options,
	})
}

func (r *runner) buildDevImageCompose(
	ctx context.Context,
	parsedConfig *config.SubstitutedConfig,
	substitutionContext *config.SubstitutionContext,
	options provider.BuildOptions,
) (*config.BuildInfo, error) {
	composeHelper, err := r.composeHelper()
	if err != nil {
		return nil, fmt.Errorf("find docker compose: %w", err)
	}

	projFiles, err := r.dockerComposeProjectFiles(parsedConfig)
	if err != nil {
		return nil, err
	}

	project, err := r.loadComposeProject(ctx, composeHelper, parsedConfig, projFiles)
	if err != nil {
		return nil, err
	}

	composeService, originalImageName, err := resolveComposeServiceImage(
		project,
		composeHelper,
		parsedConfig.Config.Service,
	)
	if err != nil {
		return nil, err
	}

	extendResult, err := r.buildAndExtendDockerCompose(ctx, &buildAndExtendParams{
		parsedConfig:        parsedConfig,
		substitutionContext: substitutionContext,
		project:             project,
		composeHelper:       composeHelper,
		composeService:      &composeService,
		globalArgs:          projFiles.composeGlobalArgs,
		featureSecretsFile:  options.FeatureSecretsFile,
		pull:                options.Pull,
		noCache:             options.NoCache,
	})
	if err != nil {
		return nil, fmt.Errorf("build and extend docker-compose: %w", err)
	}

	return r.composeBuildInfo(ctx, extendResult, originalImageName, options)
}

// composeBuildInfo resolves the final image for a compose build and assembles
// the resulting BuildInfo. Compose builds do not compute a prebuild hash, so the
// image tag is used as the PrebuildHash fallback.
func (r *runner) composeBuildInfo(
	ctx context.Context,
	extendResult composeExtendResult,
	originalImageName string,
	options provider.BuildOptions,
) (*config.BuildInfo, error) {
	currentImageName := extendResult.buildImageName
	if currentImageName == "" {
		currentImageName = originalImageName
	}

	imageDetails, err := r.inspectImage(ctx, currentImageName)
	if err != nil {
		return nil, fmt.Errorf("inspect image: %w", err)
	}

	imageTag, err := r.getImageTag(ctx, imageDetails.ID)
	if err != nil {
		return nil, fmt.Errorf("inspect image: %w", err)
	}

	return &config.BuildInfo{
		ImageDetails:  imageDetails,
		ImageMetadata: extendResult.imageMetadata,
		ImageName:     extendResult.buildImageName,
		PrebuildHash:  imageTag,
		RegistryCache: options.RegistryCache,
		Tags:          options.Tag,
	}, nil
}

// dockerlessFallbackParams groups the inputs for the dockerless build fallback.
type dockerlessFallbackParams struct {
	localWorkspaceFolder     string
	containerWorkspaceFolder string
	parsedConfig             *config.SubstitutedConfig
	buildInfo                *config.ImageBuildInfo
	extendedBuildInfo        *feature.ExtendedBuildInfo
	dockerfileContent        string
	options                  provider.BuildOptions
}

func dockerlessFallback(params *dockerlessFallbackParams) (*config.BuildInfo, error) {
	parsedConfig := params.parsedConfig
	extendedBuildInfo := params.extendedBuildInfo
	options := params.options

	contextPath := config.GetContextPath(parsedConfig.Config)
	devsyInternalFolder := filepath.Join(contextPath, config.DevsyContextFeatureFolder)
	// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
	err := os.MkdirAll(devsyInternalFolder, 0o755)
	if err != nil {
		return nil, fmt.Errorf("create devsy folder: %w", err)
	}

	// build dockerfile
	devsyDockerfile, err := build.RewriteDockerfile(params.dockerfileContent, extendedBuildInfo)
	if err != nil {
		return nil, fmt.Errorf("rewrite dockerfile: %w", err)
	} else if devsyDockerfile == "" {
		devsyDockerfile = filepath.Join(devsyInternalFolder, "Dockerfile-without-features")
		err = os.WriteFile(devsyDockerfile, []byte(params.dockerfileContent), 0o600)
		if err != nil {
			return nil, fmt.Errorf("write devsy dockerfile: %w", err)
		}
	}

	// get build args and target
	containerContext, containerDockerfile := getContainerContextAndDockerfile(
		params.localWorkspaceFolder,
		params.containerWorkspaceFolder,
		contextPath,
		devsyDockerfile,
	)
	buildArgs, target := build.GetBuildArgsAndTarget(parsedConfig, extendedBuildInfo)
	return &config.BuildInfo{
		ImageMetadata: extendedBuildInfo.MetadataConfig,
		Dockerless: &config.BuildInfoDockerless{
			Context:    containerContext,
			Dockerfile: containerDockerfile,

			BuildArgs: buildArgs,
			Target:    target,

			User: params.buildInfo.User,
		},
		RegistryCache: options.RegistryCache,
		Tags:          options.Tag,
	}, nil
}

func getContainerContextAndDockerfile(
	localWorkspaceFolder, containerWorkspaceFolder, contextPath, devsyDockerfile string,
) (string, string) {
	prefixPath := path.Clean(filepath.ToSlash(localWorkspaceFolder))
	containerContext := path.Join(
		containerWorkspaceFolder,
		strings.TrimPrefix(path.Clean(filepath.ToSlash(contextPath)), prefixPath),
	)
	containerDockerfile := path.Join(
		containerWorkspaceFolder,
		strings.TrimPrefix(path.Clean(filepath.ToSlash(devsyDockerfile)), prefixPath),
	)
	return containerContext, containerDockerfile
}

func cleanupBuildInformation(c *config.DevContainerConfig) {
	contextPath := config.GetContextPath(c)
	_ = os.RemoveAll(filepath.Join(contextPath, config.DevsyContextFeatureFolder))
}

func featureSecretOpts(options provider.BuildOptions) *feature.SecretOptions {
	opts := &feature.SecretOptions{
		SecretsFile: options.FeatureSecretsFile,
		Prompter:    &feature.TerminalSecretPrompter{},
	}
	return opts
}
