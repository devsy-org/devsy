package devcontainer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/devsy-org/devsy/pkg/devcontainer/config"
	"github.com/devsy-org/devsy/pkg/driver"
	"github.com/devsy-org/devsy/pkg/driver/drivercreate"
	"github.com/devsy-org/devsy/pkg/encoding"
	"github.com/devsy-org/devsy/pkg/language"
	"github.com/devsy-org/devsy/pkg/log"
	provider2 "github.com/devsy-org/devsy/pkg/provider"
)

type Runner interface {
	Up(ctx context.Context, options UpOptions, timeout time.Duration) (*config.Result, error)

	Build(ctx context.Context, options provider2.BuildOptions) (string, error)

	Find(ctx context.Context) (*config.ContainerDetails, error)

	Command(
		ctx context.Context,
		user string,
		command string,
		stdin io.Reader,
		stdout io.Writer,
		stderr io.Writer,
	) error

	Stop(ctx context.Context) error

	Delete(ctx context.Context) error

	Logs(ctx context.Context, writer io.Writer) error
}

func NewRunner(
	agentPath, agentDownloadURL string,
	workspaceConfig *provider2.AgentWorkspaceInfo,
) (Runner, error) {
	driver, err := drivercreate.NewDriver(workspaceConfig)
	if err != nil {
		return nil, err
	}

	// we use the workspace uid as id to avoid conflicts between container names
	return &runner{
		Driver: driver,

		AgentPath:            agentPath,
		AgentDownloadURL:     agentDownloadURL,
		LocalWorkspaceFolder: workspaceConfig.ContentFolder,
		ID:                   GetRunnerIDFromWorkspace(workspaceConfig.Workspace),
		IDLabels:             workspaceConfig.CLIOptions.IDLabels,
		WorkspaceConfig:      workspaceConfig,
	}, nil
}

type runner struct {
	Driver driver.Driver

	WorkspaceConfig  *provider2.AgentWorkspaceInfo
	AgentPath        string
	AgentDownloadURL string

	LocalWorkspaceFolder string

	ID       string
	IDLabels []string
}

type UpOptions struct {
	provider2.CLIOptions

	NoBuild       bool
	ForceBuild    bool
	RegistryCache string
}

func (r *runner) Up(
	ctx context.Context,
	options UpOptions,
	timeout time.Duration,
) (*config.Result, error) {
	log.Debugf(
		"Up devcontainer for workspace '%s' with timeout %s",
		r.WorkspaceConfig.Workspace.ID,
		timeout,
	)

	substitutedConfig, substitutionContext, err := r.getSubstitutedConfig(options.CLIOptions)
	if err != nil {
		return nil, err
	}
	defer cleanupBuildInformation(substitutedConfig.Config)

	// do not run initialize command in platform mode
	if !options.Platform.Enabled {
		if err := runInitializeCommand(
			r.LocalWorkspaceFolder,
			substitutedConfig.Config,
			options.InitEnv,
		); err != nil {
			return nil, err
		}
	} else if len(substitutedConfig.Config.InitializeCommand) > 0 {
		log.Info("Skipping initializeCommand on platform")
	}

	switch {
	case isDockerFileConfig(substitutedConfig.Config),
		substitutedConfig.Config.Image != "",
		substitutedConfig.Config.ContainerID != "":
		return r.runSingleContainer(
			ctx,
			substitutedConfig,
			substitutionContext,
			options,
			timeout,
		)
	case isDockerComposeConfig(substitutedConfig.Config):
		return r.runDockerCompose(ctx, substitutedConfig, substitutionContext, options, timeout)
	default:
		return r.runDefaultContainer(ctx, options, substitutedConfig, substitutionContext, timeout)
	}
}

func (r *runner) runDefaultContainer(
	ctx context.Context,
	options UpOptions,
	substitutedConfig *config.SubstitutedConfig,
	substitutionContext *config.SubstitutionContext,
	timeout time.Duration,
) (*config.Result, error) {
	if options.FallbackImage != "" {
		log.Warn(
			"dev container config is missing one of \"image\", \"dockerFile\" or \"dockerComposeFile\" properties, " +
				"using fallback image " + options.FallbackImage,
		)

		substitutedConfig.Config.ImageContainer = config.ImageContainer{
			Image: options.FallbackImage,
		}
	} else {
		log.Warn(
			"dev container config is missing one of \"image\", \"dockerFile\" or \"dockerComposeFile\" properties, " +
				"defaulting to auto-detection",
		)

		lang, err := language.DetectLanguage(r.LocalWorkspaceFolder)
		if err != nil {
			return nil, fmt.Errorf(
				"could not detect project language and dev container config is missing one of " +
					"\"image\", \"dockerFile\" or \"dockerComposeFile\" properties",
			)
		}

		if language.MapConfig[lang] == nil {
			return nil, fmt.Errorf(
				"could not detect project language and dev container config is missing one of " +
					"\"image\", \"dockerFile\" or \"dockerComposeFile\" properties",
			)
		}
		substitutedConfig.Config.ImageContainer = language.MapConfig[lang].ImageContainer
	}

	return r.runSingleContainer(ctx, substitutedConfig, substitutionContext, options, timeout)
}

func (r *runner) Command(
	ctx context.Context,
	user string,
	command string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return r.Driver.CommandDevContainer(ctx, r.ID, user, command, stdin, stdout, stderr)
}

func (r *runner) Find(ctx context.Context) (*config.ContainerDetails, error) {
	containerDetails, err := r.Driver.FindDevContainer(ctx, r.ID)
	if err != nil {
		return nil, fmt.Errorf("find dev container: %w", err)
	}

	return containerDetails, nil
}

func (r *runner) Logs(ctx context.Context, writer io.Writer) error {
	return r.Driver.GetDevContainerLogs(ctx, r.ID, writer, writer)
}

func isDockerFileConfig(config *config.DevContainerConfig) bool {
	return config.GetDockerfile() != ""
}

// initCmdContext groups shared execution state for initializeCommand sub-commands.
type initCmdContext struct {
	shellArgs       []string
	workspaceFolder string
	extraEnvVars    []string
}

func runInitializeCommand(
	workspaceFolder string,
	conf *config.DevContainerConfig,
	extraEnvVars []string,
) error {
	if len(conf.InitializeCommand) == 0 {
		return nil
	}

	shellArgs := []string{"sh", "-c"}
	// According to the devcontainer spec, `initializeCommand` needs to be run on the host.
	// On Windows we can't assume everyone has `sh` added to their PATH so we need to use
	// Windows default shell (usually cmd.exe).
	if runtime.GOOS == "windows" {
		comSpec := os.Getenv("COMSPEC")
		if comSpec != "" {
			shellArgs = []string{comSpec, "/c"}
		} else {
			shellArgs = []string{"cmd.exe", "/c"}
		}
	}

	ctx := &initCmdContext{
		shellArgs:       shellArgs,
		workspaceFolder: workspaceFolder,
		extraEnvVars:    extraEnvVars,
	}

	if len(conf.InitializeCommand) > 1 {
		return ctx.runParallel(conf.InitializeCommand)
	}

	for name, cmd := range conf.InitializeCommand {
		if err := ctx.runSingle(name, cmd); err != nil {
			return err
		}
	}
	return nil
}

// runParallel executes all named sub-commands concurrently, collects errors, and returns them joined.
func (c *initCmdContext) runParallel(hook map[string][]string) error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	wg.Add(len(hook))
	for name, cmd := range hook {
		go func() {
			defer wg.Done()
			if err := c.runSingle(name, cmd); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("named command %q failed: %w", name, err))
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	return errors.Join(errs...)
}

// runSingle executes a single initializeCommand sub-command.
func (c *initCmdContext) runSingle(name string, cmd []string) error {
	var args []string
	if len(cmd) == 1 {
		args = make([]string, len(c.shellArgs)+1)
		copy(args, c.shellArgs)
		args[len(c.shellArgs)] = cmd[0]
	} else {
		args = cmd
	}

	log.Infof(
		"Running initializeCommand %q from devcontainer.json: '%s'",
		name,
		strings.Join(args, " "),
	)

	writer := log.Writer(log.LevelInfo)
	errwriter := log.Writer(log.LevelError)
	defer func() { _ = writer.Close() }()
	defer func() { _ = errwriter.Close() }()

	// args come from devcontainer.json initializeCommand, a trusted local config.
	c2 := exec.Command(args[0], args[1:]...) //nolint:gosec // G204
	env := c2.Environ()
	env = append(env, c.extraEnvVars...)

	c2.Stdout = writer
	c2.Stderr = errwriter
	c2.Dir = c.workspaceFolder
	c2.Env = env
	if err := c2.Run(); err != nil {
		return fmt.Errorf("initializeCommand %q failed: %w", name, err)
	}
	return nil
}

func getWorkspace(
	workspaceFolder, workspaceID string,
	conf *config.DevContainerConfig,
) (string, string) {
	if conf.WorkspaceMount != "" {
		mount := config.ParseMount(conf.WorkspaceMount)
		return conf.WorkspaceMount, mount.Target
	}

	containerMountFolder := conf.WorkspaceFolder
	if containerMountFolder == "" {
		containerMountFolder = "/workspaces/" + workspaceID
	}

	consistency := ""
	if runtime.GOOS != "linux" {
		consistency = ",consistency='consistent'"
	}

	return fmt.Sprintf(
		"type=bind,source=%s,target=%s%s",
		workspaceFolder,
		containerMountFolder,
		consistency,
	), containerMountFolder
}

func GetRunnerIDFromWorkspace(workspace *provider2.Workspace) string {
	ID := workspace.UID
	if encoding.IsLegacyUID(workspace.UID) {
		ID = workspace.ID
	}

	return ID
}
