package devcontainer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/devsy-org/devsy/pkg/compose"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"gopkg.in/yaml.v3"
)

const (
	overlayStructureLabel  = "devsy.overlay.structure"
	composeInvocationLabel = "devsy.compose.invocation"
)

func structuralSignature(c *config.DevContainerConfig) string {
	build := config.ConfigBuildOptions{}
	if c.Build != nil {
		build = *c.Build
		build.Dockerfile = ""
		build.Context = ""
	}
	resolve := func(path string) string {
		if path == "" {
			return ""
		}
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(filepath.Dir(c.Origin), filepath.FromSlash(path))
	}
	buildContext := ""
	if c.GetDockerfile() != "" {
		buildContext = config.GetContextPath(c)
	}
	files := make([]string, len(c.DockerComposeFile))
	for i, file := range c.DockerComposeFile {
		files[i] = resolve(file)
	}
	state := struct {
		Kind         string
		Image        string
		Dockerfile   string
		Context      string
		Build        config.ConfigBuildOptions
		ComposeFiles []string
		Service      string
		RunServices  []string
	}{
		Kind:         overlayConfigKind(c),
		Image:        c.Image,
		Dockerfile:   resolve(c.GetDockerfile()),
		Context:      buildContext,
		Build:        build,
		ComposeFiles: files,
		Service:      c.Service,
		RunServices:  c.RunServices,
	}
	data, _ := json.Marshal(state)
	digest := sha256.Sum256(data)
	return "v1:" + hex.EncodeToString(digest[:])
}

func hasStructuralOverlay(overlay *config.DevContainerConfig) bool {
	if overlay == nil {
		return false
	}
	if hasStructuralOverlaySources(overlay.Sources) {
		return true
	}
	return hasStructuralOverlayValues(overlay)
}

func hasStructuralOverlaySources(sources []config.ConfigSource) bool {
	for _, source := range sources {
		for _, field := range overlayStructuralFields {
			if _, ok := source.Fields[field]; ok {
				return true
			}
		}
	}
	return false
}

func hasStructuralOverlayValues(overlay *config.DevContainerConfig) bool {
	return overlay.Image != "" ||
		overlay.GetDockerfile() != "" ||
		len(overlay.DockerComposeFile) > 0 ||
		overlay.Build != nil ||
		overlay.Service != "" ||
		overlay.RunServices != nil
}

func (r *runner) teardownOverlayExisting(ctx context.Context) error {
	if r.overlayExisting == nil {
		return nil
	}
	if err := r.removeOverlayExisting(ctx); err != nil {
		return err
	}
	r.overlayExisting = nil
	return nil
}

func (r *runner) removeOverlayExisting(ctx context.Context) error {
	if isCompose, _ := getDockerComposeProject(r.overlayExisting); isCompose {
		plan := &teardownPlan{}
		r.addContainerTeardown(plan, r.overlayExisting, DeleteOptions{})
		return plan.execute(ctx)
	}
	// Preserve provider-specific recreation ownership for VM and image drivers.
	switch driver.DriverRecreateMode(r.driver) {
	case driver.RecreateDelete:
		return r.Delete(ctx, DeleteOptions{})
	case driver.RecreateStop:
		return r.driver.StopDevContainer(ctx, r.id)
	case driver.RecreateOnRun:
		return nil
	default:
		return fmt.Errorf("unsupported recreate mode %q", driver.DriverRecreateMode(r.driver))
	}
}

func (r *runner) checkOverlayRecreation(
	ctx context.Context,
	parsed *config.SubstitutedConfig,
	options UpOptions,
) error {
	r.overlayExisting = nil
	details, err := r.findOverlayExisting(ctx)
	if err != nil {
		return err
	}
	if details == nil ||
		!r.overlayStructureChanged(details, parsed, r.workspaceConfig.LastDevContainerConfig) {
		return nil
	}
	if err := validateOverlayRecreation(parsed, options); err != nil {
		return err
	}
	r.overlayExisting = details
	return nil
}

func (r *runner) findOverlayExisting(ctx context.Context) (*config.ContainerDetails, error) {
	details, err := r.driver.FindDevContainer(ctx, r.id)
	if err != nil {
		return nil, fmt.Errorf("find existing workspace before overlay selection: %w", err)
	}
	return details, nil
}

func (r *runner) overlayStructureChanged(
	details *config.ContainerDetails,
	parsed *config.SubstitutedConfig,
	last *config.DevContainerConfigWithPath,
) bool {
	previous := details.Config.Labels[overlayStructureLabel]
	if previous == "" {
		previous = r.legacyOverlayStructure(parsed, last)
	}
	if previous == "" {
		return hasStructuralOverlay(parsed.Overlay)
	}
	return previous != structuralSignature(parsed.Config)
}

func (r *runner) legacyOverlayStructure(
	parsed *config.SubstitutedConfig,
	last *config.DevContainerConfigWithPath,
) string {
	if !hasStructuralOverlay(parsed.Overlay) || last == nil || last.Config == nil {
		return ""
	}
	prior := config.CloneDevContainerConfig(last.Config)
	prior.Origin = r.lastResolvedConfigOrigin(last)
	if prior.Origin == "" {
		return ""
	}
	return structuralSignature(prior)
}

func validateOverlayRecreation(parsed *config.SubstitutedConfig, options UpOptions) error {
	if parsed.Config.ContainerID != "" {
		return fmt.Errorf("structural overlays cannot recreate an attached containerID")
	}
	if !options.Recreate {
		return fmt.Errorf(
			"workspace structural configuration changed; rerun with --recreate to apply the devcontainer overlay",
		)
	}
	return nil
}

// Asset checks happen after initialization, but before either creation path can
// remove the existing resource. Initialization may generate these assets.
func (r *runner) validateOverlayAssets(
	ctx context.Context,
	parsed *config.SubstitutedConfig,
) error {
	if !hasStructuralOverlay(parsed.Overlay) {
		return nil
	}
	switch overlayConfigKind(parsed.Config) {
	case overlayComposeSelection:
		if err := r.validateOverlayComposeAssets(ctx, parsed); err != nil {
			return err
		}
	case "dockerfile":
		if err := r.validateOverlayDockerfileAssets(parsed); err != nil {
			return err
		}
	}
	if _, ok := r.driver.(driver.ComposeDriver); overlayConfigKind(
		parsed.Config,
	) == overlayComposeSelection &&
		!ok {
		return fmt.Errorf("provider does not support compose")
	}
	return nil
}

func (r *runner) validateOverlayComposeAssets(
	ctx context.Context,
	parsed *config.SubstitutedConfig,
) error {
	helper, err := r.composeHelper()
	if err != nil {
		return err
	}
	files, err := r.dockerComposeProjectFiles(parsed)
	if err != nil {
		return err
	}
	project, err := r.loadComposeProject(ctx, helper, parsed, files)
	if err != nil {
		return err
	}
	if _, ok := project.Services[parsed.Config.Service]; !ok {
		return fmt.Errorf("overlay compose service %q is not declared", parsed.Config.Service)
	}
	return nil
}

func (r *runner) validateOverlayDockerfileAssets(parsed *config.SubstitutedConfig) error {
	if _, err := r.getDockerfilePath(parsed.Config); err != nil {
		return err
	}
	info, err := os.Stat(config.GetContextPath(parsed.Config))
	if err != nil {
		return fmt.Errorf("overlay build context: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("overlay build context must be a directory")
	}
	return nil
}

type composeInvocation struct {
	Version   int      `json:"version"`
	Args      []string `json:"args"`
	Directory string   `json:"directory"`
}

func (r *runner) composeTeardownArgs(ctx context.Context, projectName string) ([]string, error) {
	details, err := r.driver.FindDevContainer(ctx, r.id)
	if err != nil {
		return nil, err
	}
	args, found, err := persistedComposeTeardownArgs(details, projectName)
	if found || err != nil {
		return args, err
	}
	return r.legacyComposeTeardownArgs(ctx, projectName)
}

func persistedComposeTeardownArgs(
	details *config.ContainerDetails,
	projectName string,
) ([]string, bool, error) {
	if details == nil || details.Config.Labels[pkgconfig.ComposeProjectLabel] != projectName {
		return nil, false, nil
	}
	value := details.Config.Labels[composeInvocationLabel]
	if value == "" {
		return nil, false, nil
	}
	var invocation composeInvocation
	if err := json.Unmarshal([]byte(value), &invocation); err != nil {
		return nil, true, fmt.Errorf("read persisted Compose invocation: %w", err)
	}
	if err := validateComposeInvocation(invocation); err != nil {
		return nil, true, err
	}
	args := append([]string{"--project-directory", invocation.Directory}, invocation.Args...)
	return args, true, nil
}

func validateComposeInvocation(invocation composeInvocation) error {
	if err := validateComposeInvocationHeader(invocation); err != nil {
		return err
	}
	for i := 0; i < len(invocation.Args); i += 2 {
		if invocation.Args[i] != "-f" && invocation.Args[i] != "--env-file" {
			return fmt.Errorf("invalid persisted Compose argument %q", invocation.Args[i])
		}
		if invocation.Args[i+1] == "" {
			return fmt.Errorf("empty persisted Compose path")
		}
	}
	return nil
}

func validateComposeInvocationHeader(invocation composeInvocation) error {
	if invocation.Version != 1 || invocation.Directory == "" || len(invocation.Args) == 0 ||
		len(invocation.Args)%2 != 0 {
		return fmt.Errorf("invalid persisted Compose invocation")
	}
	return nil
}

func (r *runner) legacyComposeTeardownArgs(
	ctx context.Context,
	projectName string,
) ([]string, error) {
	// Legacy containers have no invocation label. Never resolve a changed overlay
	// to identify the project that is already running.
	parsed, err := r.resolveLegacyComposeConfig()
	if err != nil {
		return nil, fmt.Errorf("resolve original Compose configuration: %w", err)
	}
	legacy, err := r.resolveLegacyComposeProject(ctx, projectName, parsed)
	if err != nil {
		return nil, err
	}
	return r.composeTeardownArgsWithExistingFiles(ctx, projectName, legacy)
}

type legacyComposeProject struct {
	files   composeProjectFiles
	helper  *compose.ComposeHelper
	project *composetypes.Project
}

func (r *runner) resolveLegacyComposeProject(
	ctx context.Context,
	projectName string,
	parsed *config.SubstitutedConfig,
) (*legacyComposeProject, error) {
	if parsed == nil || len(parsed.Config.DockerComposeFile) == 0 {
		return nil, fmt.Errorf(
			"cannot establish original Compose project configuration for %q", projectName,
		)
	}
	files, err := r.dockerComposeProjectFiles(parsed)
	if err != nil {
		return nil, err
	}
	helper, err := r.composeHelper()
	if err != nil {
		return nil, err
	}
	project, err := r.loadComposeProject(ctx, helper, parsed, files)
	if err != nil {
		return nil, err
	}
	if project.Name != projectName {
		return nil, fmt.Errorf(
			"persisted configuration identifies project %q, expected %q", project.Name, projectName,
		)
	}
	return &legacyComposeProject{files: files, helper: helper, project: project}, nil
}

func (r *runner) composeTeardownArgsWithExistingFiles(
	ctx context.Context,
	projectName string,
	legacy *legacyComposeProject,
) ([]string, error) {
	files := legacy.files
	existing, err := legacy.helper.FindProjectFiles(ctx, projectName)
	if err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		files.composeGlobalArgs = nil
		for _, file := range existing {
			files.composeGlobalArgs = append(files.composeGlobalArgs, "-f", file)
		}
		for _, file := range files.envFiles {
			files.composeGlobalArgs = append(files.composeGlobalArgs, "--env-file", file)
		}
	}
	return append(
		[]string{"--project-directory", legacy.project.WorkingDir},
		files.composeGlobalArgs...), nil
}

func (r *runner) resolveLegacyComposeConfig() (*config.SubstitutedConfig, error) {
	last := r.workspaceConfig.LastDevContainerConfig
	if last == nil || last.Config == nil || len(last.Config.DockerComposeFile) == 0 {
		return nil, fmt.Errorf(
			"cannot establish original Compose configuration; no last successful configuration is available",
		)
	}
	prior := config.CloneDevContainerConfig(last.Config)
	prior.Origin = r.lastResolvedConfigOrigin(last)
	if prior.Origin == "" {
		return nil, fmt.Errorf("last successful Compose configuration has no source path")
	}
	// Current selectors describe the replacement, not the running Compose project.
	options := r.workspaceConfig.CLIOptions
	options.DevContainerImage = ""
	parsed, _, err := r.substitute(options, prior)
	return parsed, err
}

func (r *runner) lastResolvedConfigOrigin(last *config.DevContainerConfigWithPath) string {
	if last.Config.Origin != "" {
		return last.Config.Origin
	}
	if last.Path == "" {
		return ""
	}
	return filepath.Join(
		r.workspaceFolder(),
		filepath.FromSlash(r.workspaceRelativeLastConfigPath(last.Path)),
	)
}

// The generated override must include its own path in the invocation label.
// Update the YAML before Compose consumes it, after its final filename is known.
func recordComposeInvocation(path, service, directory string, args []string) error {
	// #nosec G304 -- the caller passes the generated Compose override path.
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	labels, err := composeServiceLabelsNode(&document, service)
	if err != nil {
		return err
	}
	if err := appendComposeInvocationLabel(
		labels,
		composeInvocation{Version: 1, Directory: directory, Args: args},
	); err != nil {
		return err
	}
	updated, err := yaml.Marshal(&document)
	if err != nil {
		return err
	}
	return os.WriteFile(path, updated, 0o600)
}

func composeServiceLabelsNode(document *yaml.Node, service string) (*yaml.Node, error) {
	if len(document.Content) == 0 {
		return nil, fmt.Errorf("empty compose override")
	}
	services := findComposeYAMLValue(document.Content[0], "services")
	if services == nil {
		return nil, fmt.Errorf("compose override has no services")
	}
	target := findComposeYAMLValue(services, service)
	if target == nil {
		return nil, fmt.Errorf("compose override has no service %q", service)
	}
	labels := findComposeYAMLValue(target, "labels")
	if labels == nil {
		return nil, fmt.Errorf("compose override has no labels")
	}
	return labels, nil
}

func findComposeYAMLValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; node != nil && i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func appendComposeInvocationLabel(labels *yaml.Node, invocation composeInvocation) error {
	value, err := json.Marshal(invocation)
	if err != nil {
		return err
	}
	labels.Content = append(
		labels.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: composeInvocationLabel},
		&yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: escapeComposeLabelValue(string(value)),
		},
	)
	return nil
}
