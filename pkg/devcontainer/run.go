package devcontainer

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/devsy-org/devsy/pkg/clierr"
	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/driver/drivercreate"
	"github.com/devsy-org/devsy/pkg/encoding"
	"github.com/devsy-org/devsy/pkg/language"
	"github.com/devsy-org/devsy/pkg/log"
	"github.com/devsy-org/devsy/pkg/provider"
)

// Runner drives the lifecycle of a single workspace's dev container.
type Runner interface {
	Up(ctx context.Context, options UpOptions, timeout time.Duration) (*config.Result, error)
	Build(ctx context.Context, options provider.BuildOptions) (string, error)
	Find(ctx context.Context) (*config.ContainerDetails, error)
	Command(ctx context.Context, params CommandParams) error
	Stop(ctx context.Context) error
	Delete(ctx context.Context, options DeleteOptions) error
	Logs(ctx context.Context, writer io.Writer) error
}

type DeleteOptions struct {
	RemoveVolumes bool
}

// CommandParams groups the inputs for running a command inside the dev container.
type CommandParams struct {
	User    string
	Command string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// UpOptions configures a single Up invocation.
type UpOptions struct {
	provider.CLIOptions

	// NoBuild is set by the container tunnel to force pre-built images; it is
	// distinct from the embedded CLIOptions.NoBuild used by the build command.
	NoBuild bool
	// RegistryCache is sourced from AgentWorkspaceInfo.RegistryCache (a provider
	// context option), not from CLIOptions, so it is a separate field.
	RegistryCache string
}

// toBuildOptions derives the BuildOptions for an up-triggered build, carrying
// the embedded CLIOptions and applying the up-specific overrides.
func (o UpOptions) toBuildOptions() provider.BuildOptions {
	return provider.BuildOptions{
		CLIOptions:    o.CLIOptions,
		RegistryCache: o.RegistryCache,
		NoBuild:       o.NoBuild,
	}
}

// runContainerParams groups the inputs shared by the container dispatch methods.
type runContainerParams struct {
	parsedConfig        *config.SubstitutedConfig
	substitutionContext *config.SubstitutionContext
	options             UpOptions
	timeout             time.Duration
}

type runner struct {
	driver driver.Driver

	workspaceConfig  *provider.AgentWorkspaceInfo
	agentPath        string
	agentDownloadURL string

	localWorkspaceFolder string

	id       string
	idLabels []string

	recovering bool
}

func NewRunner(
	ctx context.Context,
	agentPath, agentDownloadURL string,
	workspaceConfig *provider.AgentWorkspaceInfo,
) (Runner, error) {
	drv, err := drivercreate.NewDriver(ctx, workspaceConfig)
	if err != nil {
		return nil, err
	}

	preflightOpts := driver.PreflightOptions{
		DisableAutoStart: workspaceConfig.CLIOptions.NoAutoStart || driver.AutoStartDisabledByEnv(),
	}
	if err := driver.DriverPreflight(ctx, drv, preflightOpts); err != nil {
		return nil, err
	}

	return &runner{
		driver:               drv,
		agentPath:            agentPath,
		agentDownloadURL:     agentDownloadURL,
		localWorkspaceFolder: workspaceConfig.ContentFolder,
		id:                   GetRunnerIDFromWorkspace(workspaceConfig.Workspace),
		idLabels:             workspaceConfig.CLIOptions.IDLabels,
		workspaceConfig:      workspaceConfig,
	}, nil
}

func GetRunnerIDFromWorkspace(workspace *provider.Workspace) string {
	if encoding.IsLegacyUID(workspace.UID) {
		return workspace.ID
	}
	return workspace.UID
}

func (r *runner) Up(
	ctx context.Context,
	options UpOptions,
	timeout time.Duration,
) (*config.Result, error) {
	log.Debugf(
		"Up devcontainer for workspace %q with timeout %s",
		r.workspaceConfig.Workspace.ID,
		timeout,
	)

	substitutedConfig, substitutionContext, err := r.getSubstitutedConfig(options.CLIOptions)
	if err != nil {
		return nil, err
	}
	defer cleanupBuildInformation(substitutedConfig.Config)

	// Recovery skips initializeCommand: a failing host hook must not block the
	// recovery container. In normal mode its failure is recovery-eligible.
	if !options.Recovery {
		if err := r.runInitializeCommand(ctx, substitutedConfig.Config, options); err != nil {
			return nil, clierr.Recoverable(fmt.Errorf("initialize command: %w", err))
		}
	}

	params := &runContainerParams{
		parsedConfig:        substitutedConfig,
		substitutionContext: substitutionContext,
		options:             options,
		timeout:             timeout,
	}

	result, err := r.dispatchByConfigKind(ctx, substitutedConfig, params)
	if result != nil {
		result.RecoveryContainer = r.recovering
	}
	return result, err
}

func (r *runner) Command(ctx context.Context, params CommandParams) error {
	return r.driver.CommandDevContainer(ctx, &driver.CommandParams{
		WorkspaceID: r.id,
		User:        params.User,
		Command:     params.Command,
		Stdin:       params.Stdin,
		Stdout:      params.Stdout,
		Stderr:      params.Stderr,
	})
}

func (r *runner) Find(ctx context.Context) (*config.ContainerDetails, error) {
	containerDetails, err := r.driver.FindDevContainer(ctx, r.id)
	if err != nil {
		return nil, fmt.Errorf("find dev container: %w", err)
	}
	return containerDetails, nil
}

func (r *runner) Logs(ctx context.Context, writer io.Writer) error {
	return r.driver.GetDevContainerLogs(ctx, r.id, writer, writer)
}

// dispatchByConfigKind routes to the container implementation for the config's
// kind (image/Dockerfile, compose, or default/auto-detected).
func (r *runner) dispatchByConfigKind(
	ctx context.Context,
	substitutedConfig *config.SubstitutedConfig,
	params *runContainerParams,
) (*config.Result, error) {
	switch {
	case isDockerFileConfig(substitutedConfig.Config),
		substitutedConfig.Config.Image != "",
		substitutedConfig.Config.ContainerID != "":
		return r.runSingleContainer(ctx, params)
	case isDockerComposeConfig(substitutedConfig.Config):
		if params.options.Recovery {
			log.Warn(
				"recovery mode is not supported for docker-compose dev containers; proceeding without it",
			)
		}
		return r.runDockerCompose(ctx, params)
	default:
		return r.runDefaultContainer(ctx, params)
	}
}

// runInitializeCommand runs the host-side initializeCommand hook. The hook is
// never executed in platform mode.
func (r *runner) runInitializeCommand(
	ctx context.Context,
	conf *config.DevContainerConfig,
	options UpOptions,
) error {
	if options.Platform.Enabled {
		if len(conf.InitializeCommand) > 0 {
			log.Info("Skipping initializeCommand on platform")
		}
		return nil
	}
	return runInitializeCommand(ctx, r.localWorkspaceFolder, conf, options.InitEnv)
}

// runDefaultContainer handles configs missing image/dockerfile/compose by
// selecting a fallback image or auto-detecting the project language, then
// delegating to the single-container path.
func (r *runner) runDefaultContainer(
	ctx context.Context,
	params *runContainerParams,
) (*config.Result, error) {
	conf := params.parsedConfig.Config

	const missingProps = "dev container config is missing one of " +
		"\"image\", \"dockerFile\" or \"dockerComposeFile\" properties"

	if fallback := params.options.FallbackImage; fallback != "" {
		log.Warnf("%s, using fallback image %q", missingProps, fallback)
		conf.ImageContainer = config.ImageContainer{Image: fallback}
		return r.runSingleContainer(ctx, params)
	}

	log.Warn(missingProps + ", defaulting to auto-detection")

	lang, err := language.DetectLanguage(r.localWorkspaceFolder)
	if err != nil || language.MapConfig[lang] == nil {
		return nil, fmt.Errorf("could not detect project language and %s", missingProps)
	}
	conf.ImageContainer = language.MapConfig[lang].ImageContainer

	return r.runSingleContainer(ctx, params)
}

func isDockerFileConfig(config *config.DevContainerConfig) bool {
	return config.GetDockerfile() != ""
}
