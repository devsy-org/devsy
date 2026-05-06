package feature

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/devsy-org/devsy/pkg/copy"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/graph"
	"github.com/devsy-org/devsy/pkg/devcontainer/metadata"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/google/go-containerregistry/pkg/name"
)

var (
	featureSafeIDRegex1 = regexp.MustCompile(`[^\w_]`)
	featureSafeIDRegex2 = regexp.MustCompile(`^[\d_]+`)
)

const FEATURE_BASE_DOCKERFILE = `
FROM $_DEV_CONTAINERS_BASE_IMAGE AS dev_containers_target_stage

USER root

COPY ./` + config.DevsyContextFeatureFolder + `/ /tmp/build-features/
RUN chmod -R 0755 /tmp/build-features && ls /tmp/build-features

#{featureLayer}

ARG _DEV_CONTAINERS_IMAGE_USER=root
USER $_DEV_CONTAINERS_IMAGE_USER
`

type ExtendedBuildInfo struct {
	Features          []*config.FeatureSet
	FeaturesBuildInfo *BuildInfo

	MetadataConfig *config.ImageMetadataConfig
	MetadataLabel  string
}

type BuildInfo struct {
	FeaturesFolder          string
	DockerfileContent       string
	OverrideTarget          string
	DockerfilePrefixContent string
	BuildArgs               map[string]string
}

type ExtendedBuildParams struct {
	Ctx                *config.SubstitutionContext
	ImageBuildInfo     *config.ImageBuildInfo
	Target             string
	DevContainerConfig *config.SubstitutedConfig
	ForceBuild         bool
	SecretOpts         *SecretOptions
}

func GetExtendedBuildInfo(params *ExtendedBuildParams) (*ExtendedBuildInfo, error) {
	ctx := params.Ctx
	imageBuildInfo := params.ImageBuildInfo
	target := params.Target
	devContainerConfig := params.DevContainerConfig
	forceBuild := params.ForceBuild
	secretOpts := params.SecretOpts
	features, err := fetchFeatures(devContainerConfig.Config, forceBuild, secretOpts)
	if err != nil {
		return nil, fmt.Errorf("fetch features: %w", err)
	}

	mergedImageMetadataConfig, err := metadata.GetDevContainerMetadata(
		ctx,
		imageBuildInfo.Metadata,
		devContainerConfig,
		features,
	)
	if err != nil {
		return nil, fmt.Errorf("get dev container metadata: %w", err)
	}

	marshalled, err := metadata.MarshalImageMetadata(mergedImageMetadataConfig.Raw)
	if err != nil {
		return nil, err
	}

	// no features?
	if len(features) == 0 {
		return &ExtendedBuildInfo{
			MetadataLabel:  string(marshalled),
			MetadataConfig: mergedImageMetadataConfig,
		}, nil
	}

	contextPath := config.GetContextPath(devContainerConfig.Config)
	effectiveImageBuildInfo := *imageBuildInfo
	effectiveImageBuildInfo.Metadata = mergedImageMetadataConfig

	buildInfo, err := getFeatureBuildOptions(
		contextPath,
		&effectiveImageBuildInfo,
		target,
		features,
	)
	if err != nil {
		return nil, err
	}

	return &ExtendedBuildInfo{
		Features:          features,
		FeaturesBuildInfo: buildInfo,
		MetadataConfig:    mergedImageMetadataConfig,
		MetadataLabel:     string(marshalled),
	}, nil
}

func getFeatureBuildOptions(
	contextPath string,
	imageBuildInfo *config.ImageBuildInfo,
	target string,
	features []*config.FeatureSet,
) (*BuildInfo, error) {
	containerUser, remoteUser := findContainerUsers(
		imageBuildInfo.Metadata,
		"",
		imageBuildInfo.User,
	)

	// copy features
	featureFolder := filepath.Join(contextPath, config.DevsyContextFeatureFolder)
	err := copyFeaturesToDestination(features, featureFolder)
	if err != nil {
		return nil, err
	}

	// write devcontainer-features.builtin.env, its important to have a terminating \n here as we append to that file later
	err = os.WriteFile(
		filepath.Join(featureFolder, "devcontainer-features.builtin.env"),
		[]byte(`_CONTAINER_USER=`+containerUser+`
_REMOTE_USER=`+remoteUser+"\n"),
		0o600,
	)
	if err != nil {
		return nil, err
	}

	// prepare dockerfile
	dockerfileContent := strings.ReplaceAll(
		FEATURE_BASE_DOCKERFILE,
		"#{featureLayer}",
		getFeatureLayers(containerUser, remoteUser, features),
	)
	// get build syntax from Dockerfile or use default
	syntax := "docker.io/docker/dockerfile:1.4"
	if imageBuildInfo.Dockerfile != nil && imageBuildInfo.Dockerfile.Syntax != "" {
		syntax = imageBuildInfo.Dockerfile.Syntax
	}
	dockerfilePrefix := fmt.Sprintf(`
# syntax=%s
ARG _DEV_CONTAINERS_BASE_IMAGE=placeholder`, syntax)

	return &BuildInfo{
		FeaturesFolder:          featureFolder,
		DockerfileContent:       dockerfileContent,
		DockerfilePrefixContent: dockerfilePrefix,
		OverrideTarget:          "dev_containers_target_stage",
		BuildArgs: map[string]string{
			"_DEV_CONTAINERS_BASE_IMAGE": target,
			"_DEV_CONTAINERS_IMAGE_USER": imageBuildInfo.User,
		},
	}, nil
}

func copyFeaturesToDestination(features []*config.FeatureSet, targetDir string) error {
	// make sure the folder doesn't exist initially
	_ = os.RemoveAll(targetDir)
	for i, feature := range features {
		featureDir := filepath.Join(targetDir, strconv.Itoa(i))
		// #nosec G301 -- TODO Consider using a more secure permission setting and ownership if needed.
		err := os.MkdirAll(featureDir, 0o755)
		if err != nil {
			return err
		}

		err = copy.Directory(feature.Folder, featureDir)
		if err != nil {
			return fmt.Errorf("copy feature %s: %w", feature.ConfigID, err)
		}

		// copy feature folder
		envPath := filepath.Join(featureDir, "devcontainer-features.env")
		variables := getFeatureEnvVariables(feature.Config, feature.Options)
		err = os.WriteFile(envPath, []byte(strings.Join(variables, "\n")), 0o600)
		if err != nil {
			return fmt.Errorf("write variables of feature %s: %w", feature.ConfigID, err)
		}

		installWrapperPath := filepath.Join(featureDir, "devcontainer-features-install.sh")
		installWrapperContent := getFeatureInstallWrapperScript(
			feature.ConfigID,
			feature.Config,
			variables,
		)
		err = os.WriteFile(installWrapperPath, []byte(installWrapperContent), 0o600)
		if err != nil {
			return fmt.Errorf(
				"write install wrapper script for feature %s: %w",
				feature.ConfigID,
				err,
			)
		}
	}

	return nil
}

func getFeatureSafeID(featureID string) string {
	return strings.ToUpper(
		featureSafeIDRegex2.ReplaceAllString(
			featureSafeIDRegex1.ReplaceAllString(featureID, "_"),
			"_",
		),
	)
}

func getFeatureLayers(containerUser, remoteUser string, features []*config.FeatureSet) string {
	const envFile = "/tmp/build-features/devcontainer-features.builtin.env"
	var b strings.Builder
	b.WriteString("RUN \\\n")
	b.WriteString(`echo "_CONTAINER_USER_HOME=$(getent passwd ` + containerUser)
	b.WriteString(` | cut -d: -f6)" >> ` + envFile + " && \\\n")
	b.WriteString(`echo "_REMOTE_USER_HOME=$(getent passwd ` + remoteUser)
	b.WriteString(` | cut -d: -f6)" >> ` + envFile + "\n\n")

	for i, feature := range features {
		b.WriteString(generateContainerEnvs(feature))
		b.WriteString("\nRUN cd /tmp/build-features/" + strconv.Itoa(i) + " \\\n")
		b.WriteString("&& chmod +x ./devcontainer-features-install.sh \\\n")
		b.WriteString("&& ./devcontainer-features-install.sh\n\n")
	}

	return b.String()
}

var containerEnvVarRegexp = regexp.MustCompile(`\$\{containerEnv:([^}]+)\}`)

func generateContainerEnvs(feature *config.FeatureSet) string {
	result := []string{}
	if len(feature.Config.ContainerEnv) == 0 {
		return ""
	}

	for k, v := range feature.Config.ContainerEnv {
		v = containerEnvVarRegexp.ReplaceAllString(v, "${$1}")
		result = append(result, fmt.Sprintf("ENV %s=%s", k, v))
	}
	return strings.Join(result, "\n")
}

func findContainerUsers(
	baseImageMetadata *config.ImageMetadataConfig,
	composeServiceUser, imageUser string,
) (string, string) {
	containerUser, remoteUser := usersFromMetadata(baseImageMetadata)
	containerUser = applyUserFallback(containerUser, composeServiceUser, imageUser)
	remoteUser = applyUserFallback(remoteUser, composeServiceUser, imageUser)
	return containerUser, remoteUser
}

func usersFromMetadata(meta *config.ImageMetadataConfig) (string, string) {
	reversed := config.ReverseSlice(meta.Config)
	containerUser := ""
	remoteUser := ""
	for _, imageMetadata := range reversed {
		if containerUser == "" && imageMetadata.ContainerUser != "" {
			containerUser = imageMetadata.ContainerUser
		}
		if remoteUser == "" && imageMetadata.RemoteUser != "" {
			remoteUser = imageMetadata.RemoteUser
		}
	}
	return containerUser, remoteUser
}

// ResolveFeatureOrder parses the features in a DevContainerConfig, resolves their
// dependencies, and returns them in topological install order.
func ResolveFeatureOrder(
	devContainerConfig *config.DevContainerConfig,
) ([]*config.FeatureSet, error) {
	return fetchFeatures(devContainerConfig, false, nil)
}

func applyUserFallback(user, composeServiceUser, imageUser string) string {
	if user != "" {
		return user
	}
	if composeServiceUser != "" {
		return composeServiceUser
	}
	return imageUser
}

func fetchFeatures(
	devContainerConfig *config.DevContainerConfig,
	forceBuild bool,
	secretOpts *SecretOptions,
) ([]*config.FeatureSet, error) {
	processor := &featureProcessor{
		devContainerConfig: devContainerConfig,
		forceBuild:         forceBuild,
		secretOpts:         secretOpts,
	}

	userFeatures, err := getUserFeatures(processor, devContainerConfig)
	if err != nil {
		return nil, err
	}

	allFeatures, err := resolveDependencies(processor, userFeatures)
	if err != nil {
		return nil, fmt.Errorf("resolve dependencies: %w", err)
	}

	featureSets := make([]*config.FeatureSet, 0, len(allFeatures))
	for _, featureSet := range allFeatures {
		featureSets = append(featureSets, featureSet)
	}

	featureSets, err = getSortedFeatureSets(devContainerConfig, featureSets)
	if err != nil {
		return nil, fmt.Errorf("failed to get sorted feature sets: %w", err)
	}

	return featureSets, nil
}

func getUserFeatures(
	processor *featureProcessor,
	devContainerConfig *config.DevContainerConfig,
) (map[string]*config.FeatureSet, error) {
	userFeatures := map[string]*config.FeatureSet{}
	for featureID, featureOptions := range devContainerConfig.Features {
		featureSet, err := processor.processFeature(featureID, featureOptions)
		if err != nil {
			return nil, fmt.Errorf("process feature %s: %w", featureID, err)
		}
		key := featureDeduplicationKey(featureSet.ConfigID, featureSet.Version)
		userFeatures[key] = featureSet
	}
	return userFeatures, nil
}

type featureProcessor struct {
	devContainerConfig *config.DevContainerConfig
	forceBuild         bool
	secretOpts         *SecretOptions
}

func (p *featureProcessor) processFeature(
	featureID string,
	featureOptions any,
) (*config.FeatureSet, error) {
	featureFolder, err := ProcessFeatureID(featureID, p.devContainerConfig, p.forceBuild)
	if err != nil {
		return nil, fmt.Errorf("process feature ID %s: %w", featureID, err)
	}

	log.Debugf("parse dev container feature in %s", featureFolder)
	featureConfig, err := config.ParseDevContainerFeature(featureFolder)
	if err != nil {
		return nil, fmt.Errorf("parse feature: %w", err)
	}

	if annotations := LoadOCIAnnotations(featureFolder); annotations != nil {
		featureConfig.Annotations = annotations
	}

	if err := ValidateFeatureOptions(featureID, featureConfig, featureOptions); err != nil {
		return nil, err
	}

	resolvedOptions, err := resolveSecretsForFeature(
		featureID,
		featureConfig,
		featureOptions,
		p.secretOpts,
	)
	if err != nil {
		return nil, err
	}

	return &config.FeatureSet{
		ConfigID: normalizeFeatureID(featureID),
		Version:  extractVersionFromFeatureID(featureID),
		Folder:   featureFolder,
		Config:   featureConfig,
		Options:  resolvedOptions,
	}, nil
}

func resolveSecretsForFeature(
	featureID string,
	featureCfg *config.FeatureConfig,
	featureOptions any,
	secretOpts *SecretOptions,
) (any, error) {
	if featureCfg == nil || len(featureCfg.Options) == 0 {
		return featureOptions, nil
	}

	hasSecrets := false
	for _, opt := range featureCfg.Options {
		if opt.Type == optionTypeSecret {
			hasSecrets = true
			break
		}
	}
	if !hasSecrets {
		return featureOptions, nil
	}

	userMap := toOptionsMap(featureOptions, featureCfg)
	if userMap == nil {
		userMap = map[string]any{}
	}

	resolved, err := ResolveSecretOptions(featureID, featureCfg, userMap, secretOpts)
	if err != nil {
		return nil, err
	}

	return resolved, nil
}

type featureDependencyResolver struct {
	features  map[string]*config.FeatureSet
	resolved  map[string]*config.FeatureSet
	visiting  map[string]bool
	processor *featureProcessor
	legacyMap map[string]string
}

func (r *featureDependencyResolver) findByConfigID(configID string) (string, *config.FeatureSet) {
	if fs, ok := r.features[configID]; ok {
		return configID, fs
	}
	for key, fs := range r.features {
		if fs.ConfigID == configID {
			return key, fs
		}
	}
	return "", nil
}

func (r *featureDependencyResolver) resolveFeatureDependency( //nolint:cyclop
	featureID string,
	featureSet *config.FeatureSet,
) error {
	if r.resolved[featureID] != nil {
		return nil // Already resolved
	}

	if r.visiting[featureID] {
		return fmt.Errorf("circular dependency detected involving feature %s", featureID)
	}

	r.visiting[featureID] = true
	defer func() { r.visiting[featureID] = false }()

	for depID, depOptions := range featureSet.Config.DependsOn {
		normalizedDepID := normalizeFeatureID(depID)
		resolvedKey, depFeatureSet := r.findByConfigID(normalizedDepID)
		if depFeatureSet == nil {
			if currentID, legacyMatch := r.legacyMap[normalizedDepID]; legacyMatch {
				log.Debugf("resolved legacy ID %s to current feature %s", depID, currentID)
				resolvedKey, depFeatureSet = r.findByConfigID(currentID)
			}
		}
		if depFeatureSet == nil {
			log.Debugf("installing dependency feature %s", depID)
			var err error
			depFeatureSet, err = r.processor.processFeature(depID, depOptions)
			if err != nil {
				return fmt.Errorf("failed to resolve dependency %s: %w", depID, err)
			}
			resolvedKey = featureDeduplicationKey(depFeatureSet.ConfigID, depFeatureSet.Version)
			r.features[resolvedKey] = depFeatureSet
			r.rebuildLegacyMap()
		}

		err := r.resolveFeatureDependency(resolvedKey, depFeatureSet)
		if err != nil {
			return err
		}
	}

	r.resolved[featureID] = featureSet
	return nil
}

func (r *featureDependencyResolver) rebuildLegacyMap() {
	r.legacyMap = buildLegacyIDMap(r.features)
}

func resolveDependencies(
	processor *featureProcessor,
	features map[string]*config.FeatureSet,
) (map[string]*config.FeatureSet, error) {
	resolver := &featureDependencyResolver{
		features:  features,
		resolved:  make(map[string]*config.FeatureSet),
		visiting:  make(map[string]bool),
		processor: processor,
		legacyMap: buildLegacyIDMap(features),
	}

	for featureID, featureSet := range features {
		err := resolver.resolveFeatureDependency(featureID, featureSet)
		if err != nil {
			return nil, err
		}
	}

	return resolver.resolved, nil
}

func buildLegacyIDMap(features map[string]*config.FeatureSet) map[string]string {
	legacyMap := make(map[string]string)
	for configID, featureSet := range features {
		if featureSet.Config == nil {
			continue
		}
		for _, legacyID := range featureSet.Config.LegacyIds {
			legacyMap[normalizeFeatureID(legacyID)] = configID
		}
	}
	return legacyMap
}

func normalizeFeatureID(featureID string) string {
	ref, err := name.ParseReference(featureID)
	if err != nil {
		return featureID
	}

	tag, ok := ref.(name.Tag)
	if ok {
		return tag.Repository.Name()
	}

	return ref.String()
}

func extractVersionFromFeatureID(featureID string) string {
	ref, err := name.ParseReference(featureID)
	if err != nil {
		return ""
	}

	tag, ok := ref.(name.Tag)
	if !ok {
		return ""
	}

	return normalizeVersion(tag.TagStr())
}

func normalizeVersion(version string) string {
	if version == "latest" || version == "" {
		return ""
	}
	return strings.TrimPrefix(version, "v")
}

func featureDeduplicationKey(configID, version string) string {
	if version == "" {
		return configID
	}
	return configID + ":" + version
}

func getSortedFeatureSets(
	devContainer *config.DevContainerConfig,
	featureSets []*config.FeatureSet,
) ([]*config.FeatureSet, error) {
	if len(devContainer.OverrideFeatureInstallOrder) == 0 {
		return getOrderedFeatureSets(featureSets)
	}

	featureLookup := buildFeatureLookupMap(featureSets)
	priority := buildOverridePriority(devContainer.OverrideFeatureInstallOrder, featureLookup)

	if err := validateOverrideOrder(priority, featureSets, featureLookup); err != nil {
		return nil, err
	}

	return getOrderedFeatureSetsWithPriority(featureSets, priority)
}

func buildOverridePriority(
	overrideOrder []string,
	featureLookup map[string]*config.FeatureSet,
) map[string]int {
	priority := make(map[string]int)
	for i, id := range overrideOrder {
		if _, exists := featureLookup[id]; exists {
			priority[id] = i
			continue
		}
		normalizedID := normalizeFeatureID(id)
		key := findKeyInLookupByConfigID(normalizedID, featureLookup)
		if key != "" {
			priority[key] = i
		}
	}
	return priority
}

func validateOverrideOrder(
	priority map[string]int,
	featureSets []*config.FeatureSet,
	featureLookup map[string]*config.FeatureSet,
) error {
	for _, feature := range featureSets {
		featureKey := featureDeduplicationKey(feature.ConfigID, feature.Version)
		featurePriority, featureHasPriority := priority[featureKey]
		if !featureHasPriority {
			continue
		}

		for depID := range feature.Config.DependsOn {
			depKey := resolveDepConfigID(depID, featureLookup)
			if depKey == "" {
				continue
			}
			depPriority, depHasPriority := priority[depKey]
			if !depHasPriority {
				continue
			}
			if featurePriority < depPriority {
				return fmt.Errorf(
					"overrideFeatureInstallOrder places %q (position %d)"+
						" before its dependency %q (position %d)",
					featureKey,
					featurePriority,
					depKey,
					depPriority,
				)
			}
		}
	}
	return nil
}

func resolveDepConfigID(depID string, featureLookup map[string]*config.FeatureSet) string {
	if _, exists := featureLookup[depID]; exists {
		return depID
	}
	normalizedID := normalizeFeatureID(depID)
	return findKeyInLookupByConfigID(normalizedID, featureLookup)
}

func getOrderedFeatureSetsWithPriority(
	features []*config.FeatureSet,
	priority map[string]int,
) ([]*config.FeatureSet, error) {
	dependencyGraph, err := buildFeatureDependencyGraph(features)
	if err != nil {
		return nil, err
	}
	return dependencyGraph.SortWithPriority(priority)
}

func extractFeatureByID(features []*config.FeatureSet, featureID string) *config.FeatureSet {
	version := extractVersionFromFeatureID(featureID)
	normalizedID := normalizeFeatureID(featureID)
	for _, feature := range features {
		if (feature.ConfigID == featureID || feature.ConfigID == normalizedID) &&
			feature.Version == version {
			return feature
		}
	}
	return nil
}

func containsFeature(features []*config.FeatureSet, featureID string) bool {
	version := extractVersionFromFeatureID(featureID)
	normalizedID := normalizeFeatureID(featureID)
	for _, feature := range features {
		if (feature.ConfigID == featureID || feature.ConfigID == normalizedID) &&
			feature.Version == version {
			return true
		}
	}
	return false
}

func getOrderedFeatureSets(features []*config.FeatureSet) ([]*config.FeatureSet, error) {
	dependencyGraph, err := buildFeatureDependencyGraph(features)
	if err != nil {
		return nil, err
	}

	return dependencyGraph.Sort()
}

func buildFeatureDependencyGraph(
	features []*config.FeatureSet,
) (*graph.Graph[*config.FeatureSet], error) {
	g := graph.NewGraph[*config.FeatureSet]()
	featureLookup := buildFeatureLookupMap(features)
	if err := g.AddNodes(featureLookup); err != nil {
		return nil, fmt.Errorf("failed to add features: %w", err)
	}

	for _, feature := range features {
		if err := addHardDependencies(g, feature, featureLookup); err != nil {
			return nil, err
		}

		if err := addSoftDependencies(g, feature, featureLookup); err != nil {
			return nil, err
		}
	}

	return g, nil
}

func findKeyInLookupByConfigID(
	configID string,
	featureLookup map[string]*config.FeatureSet,
) string {
	if _, exists := featureLookup[configID]; exists {
		return configID
	}
	for key, fs := range featureLookup {
		if fs.ConfigID == configID {
			return key
		}
	}
	return ""
}

func addHardDependencies(
	g *graph.Graph[*config.FeatureSet],
	feature *config.FeatureSet,
	featureLookup map[string]*config.FeatureSet,
) error {
	featureKey := featureDeduplicationKey(feature.ConfigID, feature.Version)
	for id := range feature.Config.DependsOn {
		normalizedID := normalizeFeatureID(id)
		depKey := findKeyInLookupByConfigID(normalizedID, featureLookup)
		if depKey != "" {
			if err := g.AddEdge(depKey, featureKey); err != nil {
				return err
			}
		}
	}
	return nil
}

func addSoftDependencies(
	g *graph.Graph[*config.FeatureSet],
	feature *config.FeatureSet,
	featureLookup map[string]*config.FeatureSet,
) error {
	featureKey := featureDeduplicationKey(feature.ConfigID, feature.Version)
	for _, id := range feature.Config.InstallsAfter {
		normalizedID := normalizeFeatureID(id)
		depKey := findKeyInLookupByConfigID(normalizedID, featureLookup)
		if depKey == "" {
			continue
		}

		if hasHardDependency(feature, id, normalizedID) {
			continue
		}

		if err := g.AddEdge(depKey, featureKey); err != nil {
			return err
		}
	}
	return nil
}

func buildFeatureLookupMap(features []*config.FeatureSet) map[string]*config.FeatureSet {
	lookup := make(map[string]*config.FeatureSet, len(features))
	for _, feature := range features {
		key := featureDeduplicationKey(feature.ConfigID, feature.Version)
		lookup[key] = feature
	}
	return lookup
}

func hasHardDependency(feature *config.FeatureSet, originalID, normalizedID string) bool {
	if _, ok := feature.Config.DependsOn[originalID]; ok {
		return true
	}
	if _, ok := feature.Config.DependsOn[normalizedID]; ok {
		return true
	}
	for id := range feature.Config.DependsOn {
		if normalizeFeatureID(id) == normalizedID {
			return true
		}
	}
	return false
}
