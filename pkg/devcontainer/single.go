package devcontainer

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"strings"

	"github.com/devsy-org/devsy/pkg/agent/delivery"
	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/command"
	pkgconfig "github.com/devsy-org/devsy/pkg/config"
	"github.com/devsy-org/devsy/pkg/daemon/agent"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/devcontainer/metadata"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/language"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/status"
	"github.com/devsy-org/devsy/pkg/telemetry/distinctid"
)

var dockerlessImage = "ghcr.io/devsy-org/dockerless:0.2.0"

var defaultRecoveryImage = language.MapConfig[language.None].Image

const (
	DevsyExtraEnvVar            = "DEVSY"
	RemoteContainersExtraEnvVar = "REMOTE_CONTAINERS"

	shShellPath              = "/bin/sh"
	startScriptEchoStatement = "echo Container started"
	startScriptTrapStatement = `trap "exit 0" 15`
)

// joinShellStatements joins statements into a single-line script, avoiding
// embedded newlines that get mangled by YAML/log formatting.
func joinShellStatements(statements ...string) string {
	return strings.Join(statements, "; ")
}

// DefaultEntrypoint waits for the devsy agent binary to become available
// before handing off to the container daemon.
//
// The env var name here must match pkgconfig.EnvAgentPath (set by agent
// delivery in pkg/agent/delivery/local_docker.go). It cannot reference the
// Go constant from a shell string, so the coupling is maintained by convention.
var DefaultEntrypoint = joinShellStatements(
	`while ! command -v "${DEVSY_AGENT_PATH:-/usr/local/bin/devsy}" >/dev/null 2>&1`,
	`do echo "waiting for devsy agent to be available"`,
	"sleep 1",
	"done",
	`exec "${DEVSY_AGENT_PATH:-/usr/local/bin/devsy}" internal agent container daemon`,
)

// resolvedContainer holds the outputs that every code path through
// runSingleContainer must produce before handing off to setupContainer.
// Using a struct makes it impossible to forget either field.
type resolvedContainer struct {
	details      *config.ContainerDetails
	mergedConfig *config.MergedDevContainerConfig
	hostWarnings []string
}

// resolveParams bundles the arguments shared by the container resolution methods.
type resolveParams struct {
	parsedConfig        *config.SubstitutedConfig
	substitutionContext *config.SubstitutionContext
	options             UpOptions
}

func (r *runner) runSingleContainer(
	ctx context.Context,
	runParams *runContainerParams,
) (*config.Result, error) {
	parsedConfig := runParams.parsedConfig
	substitutionContext := runParams.substitutionContext
	options := runParams.options
	timeout := runParams.timeout

	log.Debugf("starting devcontainer for workspace %s", r.id)

	substitutionContext.Userns = options.Userns
	substitutionContext.UidMap = options.UidMap
	substitutionContext.GidMap = options.GidMap

	containerDetails, err := r.findExistingDevContainer(ctx)
	if err != nil {
		return nil, err
	}

	params := &resolveParams{
		parsedConfig:        parsedConfig,
		substitutionContext: substitutionContext,
		options:             options,
	}

	// Overlaps with the container build/start below instead of waiting.
	go r.prefetchAgentBinary(ctx)

	// Resolve container: ensure we have a running container with merged config.
	status.Enter(r.reporter, status.PhaseStartingContainer, "")
	resolved, err := r.resolveContainer(ctx, params, containerDetails)
	if err != nil {
		status.Fail(r.reporter, status.PhaseStartingContainer, err)
		return nil, err
	}
	status.Leave(r.reporter, status.PhaseStartingContainer, "")

	return r.setupContainer(ctx, &setupContainerParams{
		rawConfig:           parsedConfig.Raw,
		containerDetails:    resolved.details,
		mergedConfig:        resolved.mergedConfig,
		substitutionContext: substitutionContext,
		timeout:             timeout,
		hostWarnings:        resolved.hostWarnings,
	})
}

// resolveContainer ensures a running container with merged config, either by
// reusing the existing container or creating a new one. Recreating a container
// not created by Devsy (i.e. one with an explicit ContainerID) is rejected.
func (r *runner) resolveContainer(
	ctx context.Context,
	params *resolveParams,
	containerDetails *config.ContainerDetails,
) (*resolvedContainer, error) {
	options := params.options

	if options.Recreate && params.parsedConfig.Config.ContainerID != "" {
		return nil, fmt.Errorf("cannot recreate container not created by Devsy")
	}

	if options.Recreate || containerDetails == nil {
		return r.resolveNewContainer(ctx, params)
	}

	substitutionContext := params.substitutionContext
	if actual := workspaceMountDestination(containerDetails); actual != "" &&
		actual != substitutionContext.ContainerWorkspaceFolder {
		log.Infof(
			"container workspace mount is %s, updating from computed %s",
			actual, substitutionContext.ContainerWorkspaceFolder,
		)
		substitutionContext.ContainerWorkspaceFolder = actual
	}
	return r.resolveExistingContainer(ctx, containerDetails, params)
}

// findExistingDevContainer looks up the dev container, first checking that the
// configured docker command exists. Returns nil details (without error) when
// docker is unavailable.
func (r *runner) findExistingDevContainer(
	ctx context.Context,
) (*config.ContainerDetails, error) {
	dockerCmd := "docker"
	if r.workspaceConfig.Agent.Docker.Path != "" {
		dockerCmd = r.workspaceConfig.Agent.Docker.Path
	}
	if !command.Exists(dockerCmd) {
		return nil, nil
	}

	containerDetails, err := r.driver.FindDevContainer(ctx, r.id)
	if err != nil {
		return nil, fmt.Errorf("find dev container: %w", err)
	}
	return containerDetails, nil
}

// resolveExistingContainer handles the case where a container already exists.
// It starts the container if stopped, merges configuration from container
// metadata, and optionally reprovisions. Returns fresh container details.
func (r *runner) resolveExistingContainer(
	ctx context.Context,
	containerDetails *config.ContainerDetails,
	p *resolveParams,
) (*resolvedContainer, error) {
	if isRecoveryContainer(containerDetails) {
		r.recovering = true
	}

	containerDetails, err := r.ensureRunning(ctx, containerDetails)
	if err != nil {
		return nil, err
	}

	// For non-managed containers with a workingDir, use it as the workspace folder.
	if p.parsedConfig.Config.ContainerID != "" && containerDetails.Config.WorkingDir != "" {
		p.substitutionContext.ContainerWorkspaceFolder = containerDetails.Config.WorkingDir
	}

	mergedConfig, err := r.mergeExistingContainerConfig(ctx, containerDetails, p)
	if err != nil {
		return nil, err
	}

	containerDetails, err = r.reprovisionIfNeeded(ctx, containerDetails)
	if err != nil {
		return nil, err
	}

	return &resolvedContainer{
		details:      containerDetails,
		mergedConfig: mergedConfig,
	}, nil
}

// ensureRunning starts the container if it is not running and returns
// fresh container details.
func (r *runner) ensureRunning(
	ctx context.Context,
	containerDetails *config.ContainerDetails,
) (*config.ContainerDetails, error) {
	if strings.ToLower(containerDetails.State.Status) == containerStatusRunning {
		return containerDetails, nil
	}

	if err := r.driver.StartDevContainer(ctx, r.id); err != nil {
		return nil, err
	}
	return r.findRunningContainerOrFail(ctx, "start")
}

// mergeExistingContainerConfig extracts image metadata from the running
// container and merges it with the parsed devcontainer configuration.
func (r *runner) mergeExistingContainerConfig(
	ctx context.Context,
	containerDetails *config.ContainerDetails,
	p *resolveParams,
) (*config.MergedDevContainerConfig, error) {
	imageMetadataConfig, err := metadata.GetImageMetadataFromContainer(
		containerDetails,
		p.substitutionContext,
	)
	if err != nil {
		return nil, err
	}

	if p.options.ExtraDevContainerPath != "" {
		if imageMetadataConfig == nil {
			imageMetadataConfig = &config.ImageMetadataConfig{}
		}
		extraConfig, parseErr := config.ParseDevContainerJSONFile(
			ctx,
			p.options.ExtraDevContainerPath,
		)
		if parseErr != nil {
			return nil, parseErr
		}
		config.AddConfigToImageMetadata(extraConfig, imageMetadataConfig)
	}

	mergedConfig, err := config.MergeConfiguration(
		p.parsedConfig.Config,
		imageMetadataConfig.Config,
	)
	if err != nil {
		return nil, fmt.Errorf("merge config: %w", err)
	}

	if err := config.MergeExtraRemoteEnv(
		ctx, mergedConfig, p.options.ExtraDevContainerPath,
	); err != nil {
		return nil, err
	}

	return mergedConfig, nil
}

// reprovisionIfNeeded re-runs the devcontainer via the driver if the driver
// supports reprovisioning, and returns fresh container details.
func (r *runner) reprovisionIfNeeded(
	ctx context.Context,
	containerDetails *config.ContainerDetails,
) (*config.ContainerDetails, error) {
	d, ok := r.driver.(driver.ReprovisioningDriver)
	if !ok || !d.CanReprovision() {
		return containerDetails, nil
	}

	// ReprovisioningDriver embeds RunOptionsDriver, so d can re-run the container.
	if err := d.RunDevContainer(ctx, r.id, nil); err != nil {
		return nil, fmt.Errorf("runner driver run dev container: %w", err)
	}
	return r.findRunningContainerOrFail(ctx, "reprovision")
}

// resolveNewContainer handles the case where no container exists (or recreate
// is requested). It builds the image, runs a new container, and returns fresh
// container details.
func (r *runner) resolveNewContainer(
	ctx context.Context,
	p *resolveParams,
) (*resolvedContainer, error) {
	hostWarnings, err := r.newContainerHostWarnings(p)
	if err != nil {
		return nil, err
	}

	buildInfo, mergedConfig, err := r.buildNewContainerConfig(ctx, p)
	if err != nil {
		return nil, err
	}

	r.injectDaemonEntrypoint(p, mergedConfig)

	r.attemptPreStartDelivery(ctx, mergedConfig, p, buildInfo)

	if seedErr := r.seedWorkspaceVolume(ctx, p); seedErr != nil {
		return nil, fmt.Errorf("seed workspace volume: %w", seedErr)
	}

	err = r.runContainer(ctx, p, mergedConfig, buildInfo)
	if err != nil {
		return nil, fmt.Errorf("runner run container: %w", err)
	}

	containerDetails, err := r.findRunningContainerOrFail(ctx, "creation")
	if err != nil {
		return nil, err
	}

	if w := r.lingerWarning(ctx); w != "" {
		hostWarnings = append(hostWarnings, w)
	}

	return &resolvedContainer{
		details:      containerDetails,
		mergedConfig: mergedConfig,
		hostWarnings: hostWarnings,
	}, nil
}

// attemptPreStartDelivery builds run options and attempts pre-start agent
// delivery. Failures are non-fatal: the caller falls back to post-start.
func (r *runner) attemptPreStartDelivery(
	ctx context.Context,
	mergedConfig *config.MergedDevContainerConfig,
	p *resolveParams,
	buildInfo *config.BuildInfo,
) {
	runOptions, err := r.buildRunOptionsForDelivery(mergedConfig, p.substitutionContext, buildInfo)
	if err != nil {
		return
	}
	if preStartErr := r.deliverPreStart(ctx, runOptions); preStartErr != nil {
		log.Debugf("pre-start delivery skipped or failed, will use post-start: %v", preStartErr)
	}
}

func (r *runner) lingerWarning(ctx context.Context) string {
	helperProvider, ok := r.driver.(driver.DockerHelperProvider)
	if !ok {
		return ""
	}
	helper, err := helperProvider.DockerHelper()
	if err != nil {
		return ""
	}
	return helper.LingerWarning(ctx)
}

// buildNewContainerConfig builds the image (deleting the existing container
// first when recreating) and produces the merged devcontainer config from the
// build's image metadata.
func (r *runner) buildNewContainerConfig(
	ctx context.Context,
	p *resolveParams,
) (*config.BuildInfo, *config.MergedDevContainerConfig, error) {
	activeConfig := p.parsedConfig

	buildInfo, err := r.build(
		ctx,
		activeConfig,
		p.substitutionContext,
		p.options.toBuildOptions(),
	)
	if err != nil {
		if !p.options.Recovery {
			log.Info("dev container build failed; re-run with --recovery to " +
				"start a recovery container with features and lifecycle commands disabled")
			return nil, nil, clierr.Recoverable(fmt.Errorf("build image: %w", err))
		}
		buildInfo, activeConfig, err = r.buildRecoveryContainerConfig(ctx, p, err)
		if err != nil {
			return nil, nil, err
		}
		r.recovering = true
	}

	if p.options.Recreate {
		if err := r.deleteForRecreate(ctx); err != nil {
			return nil, nil, err
		}
	}

	mergedConfig, err := config.MergeConfiguration(
		activeConfig.Config,
		buildInfo.ImageMetadata.Config,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("merge config: %w", err)
	}

	if err := config.MergeExtraRemoteEnv(
		ctx,
		mergedConfig,
		p.options.ExtraDevContainerPath,
	); err != nil {
		return nil, nil, err
	}

	return buildInfo, mergedConfig, nil
}

// buildRecoveryContainerConfig rebuilds from a stripped-down config after a
// failed build, returning the recovery build info and the config that produced it.
func (r *runner) buildRecoveryContainerConfig(
	ctx context.Context,
	p *resolveParams,
	buildErr error,
) (*config.BuildInfo, *config.SubstitutedConfig, error) {
	log.Warnf("dev container build failed: %v", buildErr)
	log.Warn("recovery mode enabled: retrying with features and lifecycle commands " +
		"disabled so the workspace can start; fix devcontainer.json and rebuild to " +
		"restore the full container")

	recoveryConfig := recoveryDevContainerConfig(p.parsedConfig)

	buildInfo, err := r.build(
		ctx,
		recoveryConfig,
		p.substitutionContext,
		p.options.toBuildOptions(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf(
			"build recovery image: %w (original build error: %v)", err, buildErr,
		)
	}

	return buildInfo, recoveryConfig, nil
}

// recoveryDevContainerConfig strips features and lifecycle hooks, keeping a
// plain image but swapping a (possibly broken) Dockerfile for a known-good image.
// isRecoveryContainer reports whether an existing container was built in
// recovery mode, read from the label stamped at run time.
func isRecoveryContainer(details *config.ContainerDetails) bool {
	return details != nil &&
		details.Config.Labels[pkgconfig.DockerRecoveryLabel] == pkgconfig.LabelValueTrue
}

func recoveryDevContainerConfig(parsed *config.SubstitutedConfig) *config.SubstitutedConfig {
	cloned := config.CloneDevContainerConfig(parsed.Config)
	cloned.Features = nil
	cloned.OverrideFeatureInstallOrder = nil
	cloned.DevContainerActions = config.DevContainerActions{}

	if cloned.Image == "" {
		cloned.DockerfileContainer = config.DockerfileContainer{}
		cloned.Image = defaultRecoveryImage
	}

	return &config.SubstitutedConfig{
		Config: cloned,
		Raw:    parsed.Raw,
	}
}

// newContainerHostWarnings validates host requirements for a new container,
// returning warnings. Unmet requirements error unless SkipHostRequirements is
// set, in which case the error is downgraded to a warning.
func (r *runner) newContainerHostWarnings(p *resolveParams) ([]string, error) {
	hostWarnings, hostErr := config.ValidateHostRequirements(
		p.parsedConfig.Config.HostRequirements,
		config.SystemHostInfo{},
		p.substitutionContext.LocalWorkspaceFolder,
	)
	if hostErr != nil {
		if !p.options.SkipHostRequirements {
			return nil, hostErr
		}
		hostWarnings = append(hostWarnings, hostErr.Error())
	}
	return hostWarnings, nil
}

// deleteForRecreate removes the existing container before recreating it.
// Docker containers are fully deleted; other drivers stop the container.
func (r *runner) deleteForRecreate(ctx context.Context) error {
	if _, ok := r.driver.(driver.ImageDriver); ok {
		if err := r.Delete(ctx, DeleteOptions{}); err != nil {
			return fmt.Errorf("delete devcontainer: %w", err)
		}
		return nil
	}

	if err := r.driver.StopDevContainer(ctx, r.id); err != nil {
		return fmt.Errorf("stop devcontainer: %w", err)
	}
	return nil
}

// injectDaemonEntrypoint adds the workspace daemon config to the container
// environment when platform configuration is provided.
func (r *runner) injectDaemonEntrypoint(
	p *resolveParams,
	mergedConfig *config.MergedDevContainerConfig,
) {
	if p.options.Platform.AccessKey == "" {
		return
	}
	log.Debugf("Platform config detected, injecting Devsy daemon entrypoint.")

	data, err := agent.GetEncodedWorkspaceDaemonConfig(
		p.options.Platform,
		r.workspaceConfig.Workspace,
		p.substitutionContext,
		mergedConfig,
	)
	if err != nil {
		log.Errorf("Failed to marshal daemon config: %v", err)
		return
	}
	mergedConfig.ContainerEnv[pkgconfig.EnvWorkspaceDaemonConfig] = data
}

// findRunningContainerOrFail re-fetches container details from the driver and
// returns an error if the container cannot be found. Use after any operation
// that changes container state (start, create, reprovision) to ensure callers
// always receive current, non-nil details.
func (r *runner) findRunningContainerOrFail(
	ctx context.Context,
	operation string,
) (*config.ContainerDetails, error) {
	details, err := r.driver.FindDevContainer(ctx, r.id)
	if err != nil {
		return nil, fmt.Errorf("find dev container after %s: %w", operation, err)
	}
	if details == nil {
		return nil, fmt.Errorf("dev container %s not found after %s", r.id, operation)
	}
	return details, nil
}

const (
	mountTypeBind    = "bind"
	workspacesPrefix = "/workspaces/"
)

// workspaceMountDestination returns the container-side destination of the
// workspace bind mount, or "" if none is found. This is used to prefer the
// actual container mount path over the recomputed one after a rename.
func workspaceMountDestination(containerDetails *config.ContainerDetails) string {
	for _, mount := range containerDetails.Mounts {
		if mount.Type == mountTypeBind && strings.HasPrefix(mount.Destination, workspacesPrefix) {
			return mount.Destination
		}
	}
	return ""
}

func (r *runner) buildRunOptionsForDelivery(
	mergedConfig *config.MergedDevContainerConfig,
	substitutionContext *config.SubstitutionContext,
	buildInfo *config.BuildInfo,
) (*driver.RunOptions, error) {
	if buildInfo.Dockerless != nil {
		return r.getDockerlessRunOptions(mergedConfig, substitutionContext, buildInfo)
	}
	return r.getRunOptions(mergedConfig, substitutionContext, buildInfo)
}

func (r *runner) deliverPreStart(ctx context.Context, runOptions *driver.RunOptions) error {
	strategy := r.newAgentDelivery()
	if strategy.Phase() != delivery.PhasePreStart {
		return fmt.Errorf("strategy phase is %s, not pre-start", strategy.Phase())
	}

	binarySource, err := r.newBinarySource()
	if err != nil {
		return fmt.Errorf("create binary source: %w", err)
	}

	arch, err := r.deliveryArch(ctx)
	if err != nil {
		return err
	}

	return strategy.DeliverPreStart(ctx, delivery.PreStartOptions{
		WorkspaceID:  r.id,
		RunOptions:   runOptions,
		BinarySource: binarySource,
		Arch:         arch,
	})
}

// seedWorkspaceVolume populates a named workspace volume from the local source
// folder when the workspace source is a local folder and workspaceMount is a
// named volume. This gives an isolated, disposable snapshot of the working
// tree. It is skipped for git/image sources, bind mounts, and volumes devsy
// does not manage. A reset removes the managed volume so it is re-seeded.
func (r *runner) seedWorkspaceVolume(ctx context.Context, p *resolveParams) error {
	if r.workspaceConfig == nil || r.workspaceConfig.Workspace == nil {
		return nil
	}
	if r.workspaceConfig.Workspace.Source.LocalFolder == "" {
		return nil
	}

	mount := parseWorkspaceMount(p.substitutionContext)
	if mount == nil || mount.Type != "volume" || mount.Source == "" {
		return nil
	}

	seeder, ok := r.newAgentDelivery().(delivery.WorkspaceVolumeSeeder)
	if !ok {
		log.Debugf("delivery strategy cannot seed workspace volumes; skipping")
		return nil
	}

	return seeder.SeedWorkspaceVolume(ctx, delivery.WorkspaceSeedOptions{
		WorkspaceID: r.id,
		VolumeName:  mount.Source,
		SourceDir:   r.localWorkspaceFolder,
		Reset:       r.workspaceConfig.CLIOptions.Reset,
	})
}

func (r *runner) runContainer(
	ctx context.Context,
	p *resolveParams,
	mergedConfig *config.MergedDevContainerConfig,
	buildInfo *config.BuildInfo,
) error {
	var err error

	// build run options for dockerless mode
	var runOptions *driver.RunOptions
	if buildInfo.Dockerless != nil {
		runOptions, err = r.getDockerlessRunOptions(mergedConfig, p.substitutionContext, buildInfo)
		if err != nil {
			return fmt.Errorf("build dockerless run options: %w", err)
		}
	} else {
		// build run options
		runOptions, err = r.getRunOptions(mergedConfig, p.substitutionContext, buildInfo)
		if err != nil {
			return fmt.Errorf("build run options: %w", err)
		}
	}

	runOptions.Env = r.addExtraEnvVars(runOptions.Env)

	// Image drivers (Docker, Apple) build and run a local OCI image.
	if imageDriver, ok := r.driver.(driver.ImageDriver); ok {
		return imageDriver.RunImageDevContainer(ctx, &driver.RunImageDevContainerParams{
			WorkspaceID:          r.id,
			Options:              runOptions,
			ParsedConfig:         withResolvedUser(p.parsedConfig.Config, mergedConfig),
			IDE:                  r.workspaceConfig.Workspace.IDE.Name,
			IDEOptions:           r.workspaceConfig.Workspace.IDE.Options,
			LocalWorkspaceFolder: r.localWorkspaceFolder,
			GPUAvailability:      r.workspaceConfig.CLIOptions.GPUAvailability,
		})
	}

	// Other drivers (Kubernetes, custom) run the devcontainer from RunOptions.
	if runDriver, ok := r.driver.(driver.RunOptionsDriver); ok {
		return runDriver.RunDevContainer(ctx, r.id, runOptions)
	}

	return fmt.Errorf("driver does not support running a devcontainer")
}

// withResolvedUser returns a copy of parsedConfig carrying the effective user
// identity from the merged config. remoteUser/containerUser/updateRemoteUserUID
// often come from image metadata rather than the raw devcontainer.json, so the
// container UID/GID remap must see the merged values or it silently skips.
func withResolvedUser(
	parsedConfig *config.DevContainerConfig,
	mergedConfig *config.MergedDevContainerConfig,
) *config.DevContainerConfig {
	resolved := config.CloneDevContainerConfig(parsedConfig)
	resolved.RemoteUser = mergedConfig.RemoteUser
	resolved.ContainerUser = mergedConfig.ContainerUser
	resolved.UpdateRemoteUserUID = mergedConfig.UpdateRemoteUserUID
	return resolved
}

// parseWorkspaceMount parses the substituted workspace mount, returning nil when
// it has been suppressed via an empty workspaceMount.
func parseWorkspaceMount(substitutionContext *config.SubstitutionContext) *config.Mount {
	if substitutionContext.WorkspaceMount == "" {
		return nil
	}
	parsed := config.ParseMount(substitutionContext.WorkspaceMount)
	return &parsed
}

// workspaceUID returns the workspace UID, or an empty string when unavailable.
func (r *runner) workspaceUID() string {
	if r.workspaceConfig != nil && r.workspaceConfig.Workspace != nil {
		return r.workspaceConfig.Workspace.UID
	}
	return ""
}

// dockerlessEnv builds the environment for a dockerless build container,
// combining the kaniko/dockerless settings with the merged container env.
func (r *runner) dockerlessEnv(
	mergedConfig *config.MergedDevContainerConfig,
	buildInfo *config.BuildInfo,
) (map[string]string, error) {
	env := map[string]string{
		"DOCKERLESS":            stringTrue,
		"DOCKERLESS_CONTEXT":    buildInfo.Dockerless.Context,
		"DOCKERLESS_DOCKERFILE": buildInfo.Dockerless.Dockerfile,
		"GODEBUG":               "http2client=0", // https://github.com/GoogleContainerTools/kaniko/issues/875
	}
	maps.Copy(env, mergedConfig.ContainerEnv)
	if buildInfo.Dockerless.Target != "" {
		env["DOCKERLESS_TARGET"] = buildInfo.Dockerless.Target
	}
	if len(buildInfo.Dockerless.BuildArgs) > 0 {
		out, err := json.Marshal(config.ObjectToList(buildInfo.Dockerless.BuildArgs))
		if err != nil {
			return nil, fmt.Errorf("marshal build args: %w", err)
		}
		env["DOCKERLESS_BUILD_ARGS"] = string(out)
	}
	return env, nil
}

func (r *runner) getDockerlessRunOptions(
	mergedConfig *config.MergedDevContainerConfig,
	substitutionContext *config.SubstitutionContext,
	buildInfo *config.BuildInfo,
) (*driver.RunOptions, error) {
	workspaceMountPtr := parseWorkspaceMount(substitutionContext)

	// add metadata as label here
	marshalled, err := metadata.MarshalImageMetadata(buildInfo.ImageMetadata.Raw)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	env, err := r.dockerlessEnv(mergedConfig, buildInfo)
	if err != nil {
		return nil, err
	}

	image := dockerlessImage
	if r.workspaceConfig != nil && r.workspaceConfig.Agent.Dockerless.Image != "" {
		image = r.workspaceConfig.Agent.Dockerless.Image
	}

	// we need to add an extra mount here, because otherwise the build config might get lost
	mounts := mergedConfig.Mounts
	mounts = append(mounts, &config.Mount{
		Type:   "volume",
		Source: "dockerless-" + r.id,
		Target: "/workspaces/.dockerless",
	})
	mounts, err = r.withSecretsMount(mounts)
	if err != nil {
		return nil, err
	}

	// build run options
	return &driver.RunOptions{
		UID:        r.workspaceUID(),
		Image:      image,
		User:       containerRootUser,
		Entrypoint: "/.dockerless/dockerless",
		Cmd: []string{
			"start",
			"--wait",
			"--entrypoint", "/.dockerless/bin/sh",
			"--cmd", "-c",
			"--cmd", GetStartScript(mergedConfig),
			"--user", buildInfo.Dockerless.User,
		},
		Env:         env,
		CapAdd:      mergedConfig.CapAdd,
		SecurityOpt: mergedConfig.SecurityOpt,
		Labels: []string{
			metadata.ImageMetadataLabel + "=" + string(marshalled),
			config.UserLabel + "=" + buildInfo.Dockerless.User,
		},
		Privileged:     mergedConfig.Privileged,
		Init:           mergedConfig.Init,
		WorkspaceMount: workspaceMountPtr,
		Mounts:         mounts,
		Userns:         substitutionContext.Userns,
		UidMap:         substitutionContext.UidMap,
		GidMap:         substitutionContext.GidMap,
	}, nil
}

func (r *runner) getRunOptions(
	mergedConfig *config.MergedDevContainerConfig,
	substitutionContext *config.SubstitutionContext,
	buildInfo *config.BuildInfo,
) (*driver.RunOptions, error) {
	workspaceMountPtr := parseWorkspaceMount(substitutionContext)

	// add metadata as label here
	marshalled, err := metadata.MarshalImageMetadata(buildInfo.ImageMetadata.Raw)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	// build labels & entrypoint
	entrypoint, cmd := GetContainerEntrypointAndArgs(mergedConfig, buildInfo.ImageDetails)

	// Get user from image details if available, otherwise use empty string
	imageUser := ""
	if buildInfo.ImageDetails != nil {
		imageUser = buildInfo.ImageDetails.Config.User
	}

	labels := []string{
		metadata.ImageMetadataLabel + "=" + string(marshalled),
		config.UserLabel + "=" + imageUser,
	}
	if r.recovering {
		labels = append(labels, pkgconfig.DockerRecoveryLabel+"="+pkgconfig.LabelValueTrue)
	}

	user := imageUser
	if mergedConfig.ContainerUser != "" {
		user = mergedConfig.ContainerUser
	}

	if err := resolveContainerEnv(mergedConfig, buildInfo); err != nil {
		return nil, err
	}

	mounts, err := r.withSecretsMount(mergedConfig.Mounts)
	if err != nil {
		return nil, err
	}

	return &driver.RunOptions{
		UID:            r.workspaceUID(),
		Image:          buildInfo.ImageName,
		ImageBuilt:     buildInfo.BuiltLocally,
		User:           user,
		Entrypoint:     entrypoint,
		Cmd:            cmd,
		Env:            mergedConfig.ContainerEnv,
		CapAdd:         mergedConfig.CapAdd,
		Labels:         labels,
		Privileged:     mergedConfig.Privileged,
		Init:           mergedConfig.Init,
		WorkspaceMount: workspaceMountPtr,
		SecurityOpt:    mergedConfig.SecurityOpt,
		Mounts:         mounts,
		Userns:         substitutionContext.Userns,
		UidMap:         substitutionContext.UidMap,
		GidMap:         substitutionContext.GidMap,
		Platform:       r.workspaceConfig.CLIOptions.RunPlatform,
	}, nil
}

// resolveContainerEnv resolves ${containerEnv:VAR} references in containerEnv
// against the image's inspected environment. ${containerWorkspaceFolder} and
// ${containerWorkspaceFolderBasename} are already substituted upstream.
func resolveContainerEnv(
	mergedConfig *config.MergedDevContainerConfig,
	buildInfo *config.BuildInfo,
) error {
	var imageEnv []string
	if buildInfo.ImageDetails != nil {
		imageEnv = buildInfo.ImageDetails.Config.Env
	}
	resolved, err := config.ResolveContainerEnvFromImage(mergedConfig.ContainerEnv, imageEnv)
	if err != nil {
		return fmt.Errorf("resolve containerEnv from image: %w", err)
	}
	mergedConfig.ContainerEnv = resolved

	return nil
}

func (r *runner) withSecretsMount(mounts []*config.Mount) ([]*config.Mount, error) {
	mount, err := r.secretsMount()
	if err != nil {
		return nil, err
	}
	if mount == nil {
		return mounts, nil
	}

	return append(mounts, mount), nil
}

// Values must never touch a persistent layer, so an unsupported driver is an
// error rather than a silent skip.
func (r *runner) secretsMount() (*config.Mount, error) {
	if r.workspaceConfig == nil || len(r.workspaceConfig.CLIOptions.SecretsMount) == 0 {
		return nil, nil
	}

	if !driver.DriverSupportsMountType(r.driver, driver.MountTypeTmpfs) {
		return nil, fmt.Errorf(
			"the current provider does not support mounting secrets as files (--secret type=mount); " +
				"use type=env instead",
		)
	}

	return &config.Mount{
		Type:   driver.MountTypeTmpfs,
		Target: config.SecretsMountDir,
		Other:  []string{"tmpfs-mode=0755"},
	}, nil
}

// add environment variables that signals that we are in a remote container
// (vscode compatibility) and specifically that we are using devsy.
func (r *runner) addExtraEnvVars(env map[string]string) map[string]string {
	if env == nil {
		env = make(map[string]string)
	}

	env[DevsyExtraEnvVar] = stringTrue
	env[RemoteContainersExtraEnvVar] = stringTrue
	r.addWorkspaceEnvVars(env)

	if os.Getenv(pkgconfig.EnvDisableTelemetry) == pkgconfig.BoolTrue {
		env[pkgconfig.EnvDisableTelemetry] = pkgconfig.BoolTrue
	} else {
		env[pkgconfig.EnvTelemetryDistinctID] = distinctid.Get()
	}

	return env
}

func (r *runner) addWorkspaceEnvVars(env map[string]string) {
	if r.workspaceConfig == nil || r.workspaceConfig.Workspace == nil {
		return
	}
	if r.workspaceConfig.Workspace.ID != "" {
		env[pkgconfig.EnvWorkspaceID] = r.workspaceConfig.Workspace.ID
	}
	if r.workspaceConfig.Workspace.UID != "" {
		env[pkgconfig.EnvWorkspaceUID] = r.workspaceConfig.Workspace.UID
	}
}

func GetStartScript(mergedConfig *config.MergedDevContainerConfig) string {
	statements := []string{
		startScriptEchoStatement,
		startScriptTrapStatement,
		`exec "$@"`,
	}
	statements = append(statements, mergedConfig.Entrypoints...)
	statements = append(statements, DefaultEntrypoint)
	return joinShellStatements(statements...)
}

func GetContainerEntrypointAndArgs(
	mergedConfig *config.MergedDevContainerConfig,
	imageDetails *config.ImageDetails,
) (string, []string) {
	cmd := []string{
		"-c",
		GetStartScript(mergedConfig),
		"-",
	} // `wait $!` allows for the `trap` to run (synchronous `sleep` would not).
	if imageDetails != nil && mergedConfig.OverrideCommand != nil &&
		!*mergedConfig.OverrideCommand {
		cmd = append(cmd, imageDetails.Config.Entrypoint...)
		cmd = append(cmd, imageDetails.Config.Cmd...)
	}
	return shShellPath, cmd
}
